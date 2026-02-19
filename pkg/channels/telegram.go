package channels

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/sipeed/picoclaw/pkg/attachments"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
	"github.com/sipeed/picoclaw/pkg/voice"
)

type TelegramChannel struct {
	*BaseChannel
	bot             *telego.Bot
	config          config.TelegramConfig
	chatIDs         map[string]int64
	transcriber     *voice.GroqTranscriber
	attachmentStore *attachments.Store
	placeholders    sync.Map // chatID -> messageID
	stopThinking    sync.Map // chatID -> thinkingCancel
}

type thinkingCancel struct {
	fn context.CancelFunc
}

func (c *thinkingCancel) Cancel() {
	if c != nil && c.fn != nil {
		c.fn()
	}
}

const telegramAttachmentMaxBytes int64 = 100 * 1024 * 1024 // 100 MB

func NewTelegramChannel(cfg config.TelegramConfig, bus *bus.MessageBus, workspace string) (*TelegramChannel, error) {
	var opts []telego.BotOption

	if cfg.Proxy != "" {
		proxyURL, parseErr := url.Parse(cfg.Proxy)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", cfg.Proxy, parseErr)
		}
		opts = append(opts, telego.WithHTTPClient(&http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
		}))
	}

	bot, err := telego.NewBot(cfg.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	base := NewBaseChannel("telegram", cfg, bus, cfg.AllowFrom)

	return &TelegramChannel{
		BaseChannel:     base,
		bot:             bot,
		config:          cfg,
		chatIDs:         make(map[string]int64),
		transcriber:     nil,
		attachmentStore: attachments.NewStore(workspace),
		placeholders:    sync.Map{},
		stopThinking:    sync.Map{},
	}, nil
}

func (c *TelegramChannel) SetTranscriber(transcriber *voice.GroqTranscriber) {
	c.transcriber = transcriber
}

func (c *TelegramChannel) Start(ctx context.Context) error {
	logger.InfoC("telegram", "Starting Telegram bot (polling mode)...")

	updates, err := c.bot.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout: 30,
	})
	if err != nil {
		return fmt.Errorf("failed to start long polling: %w", err)
	}

	c.setRunning(true)
	logger.InfoCF("telegram", "Telegram bot connected", map[string]interface{}{
		"username": c.bot.Username(),
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-updates:
				if !ok {
					logger.InfoC("telegram", "Updates channel closed, reconnecting...")
					return
				}
				if update.Message != nil {
					c.handleMessage(ctx, update)
				}
			}
		}
	}()

	return nil
}

func (c *TelegramChannel) Stop(ctx context.Context) error {
	logger.InfoC("telegram", "Stopping Telegram bot...")
	c.setRunning(false)
	return nil
}

func (c *TelegramChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram bot not running")
	}

	chatID, err := parseChatID(msg.ChatID)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	// Stop thinking animation
	if stop, ok := c.stopThinking.Load(msg.ChatID); ok {
		if cf, ok := stop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
		c.stopThinking.Delete(msg.ChatID)
	}

	// If media files are attached, send them
	if len(msg.Media) > 0 {
		// Delete placeholder if present
		if pID, ok := c.placeholders.Load(msg.ChatID); ok {
			c.placeholders.Delete(msg.ChatID)
			c.bot.DeleteMessage(ctx, &telego.DeleteMessageParams{
				ChatID:    tu.ID(chatID),
				MessageID: pID.(int),
			})
		}

		return c.sendMediaFiles(ctx, chatID, msg.Content, msg.Media)
	}

	htmlContent := markdownToTelegramHTML(msg.Content)

	// Split message if it exceeds Telegram's limit
	const telegramMaxLen = 4096
	chunks := splitLargeMessage(htmlContent, telegramMaxLen)

	// Try to edit placeholder (only for first chunk)
	if pID, ok := c.placeholders.Load(msg.ChatID); ok {
		// For progressive updates, keep the placeholder ID
		// For final responses, delete it
		if !msg.IsProgressUpdate {
			c.placeholders.Delete(msg.ChatID)
		}

		firstChunk := chunks[0]
		if len(chunks) > 1 {
			firstChunk = fmt.Sprintf("[1/%d]\n%s", len(chunks), firstChunk)
		}

		editMsg := tu.EditMessageText(tu.ID(chatID), pID.(int), firstChunk)
		editMsg.ParseMode = telego.ModeHTML

		if _, err = c.bot.EditMessageText(ctx, editMsg); err == nil {
			// Successfully edited, send remaining chunks if any
			for i := 1; i < len(chunks); i++ {
				chunkContent := fmt.Sprintf("[%d/%d]\n%s", i+1, len(chunks), chunks[i])
				tgMsg := tu.Message(tu.ID(chatID), chunkContent)
				tgMsg.ParseMode = telego.ModeHTML
				if _, err := c.bot.SendMessage(ctx, tgMsg); err != nil {
					logger.ErrorCF("telegram", "Failed to send message chunk", map[string]interface{}{
						"chunk": i + 1,
						"error": err.Error(),
					})
				}
			}
			return nil
		}
		// Fallback to new message if edit fails
		logger.WarnCF("telegram", "Failed to edit placeholder, sending new message", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// Send new message(s) - either no placeholder or edit failed
	var sentMsg *telego.Message
	for i, chunk := range chunks {
		chunkContent := chunk
		if len(chunks) > 1 {
			chunkContent = fmt.Sprintf("[%d/%d]\n%s", i+1, len(chunks), chunk)
		}

		tgMsg := tu.Message(tu.ID(chatID), chunkContent)
		tgMsg.ParseMode = telego.ModeHTML

		sent, err := c.bot.SendMessage(ctx, tgMsg)
		if err != nil {
			logger.ErrorCF("telegram", "HTML parse failed, falling back to plain text", map[string]interface{}{
				"chunk": i + 1,
				"error": err.Error(),
			})
			tgMsg.ParseMode = ""
			sent, err = c.bot.SendMessage(ctx, tgMsg)
			if err != nil {
				logger.ErrorCF("telegram", "Failed to send message chunk", map[string]interface{}{
					"chunk": i + 1,
					"error": err.Error(),
				})
				continue
			}
		}

		// Store the first sent message for progressive updates
		if i == 0 {
			sentMsg = sent
		}
	}

	// If this is a progressive update, store the message ID as the new placeholder
	if msg.IsProgressUpdate && sentMsg != nil {
		c.placeholders.Store(msg.ChatID, sentMsg.MessageID)
	}

	return nil
}

// sendMediaFiles sends local files via Telegram, choosing the appropriate method by extension.
func (c *TelegramChannel) sendMediaFiles(ctx context.Context, chatID int64, caption string, files []string) error {
	for i, filePath := range files {
		f, err := os.Open(filePath)
		if err != nil {
			logger.ErrorCF("telegram", "Failed to open file for sending", map[string]interface{}{
				"path":  filePath,
				"error": err.Error(),
			})
			continue
		}

		// Only set caption on the first file
		fileCaption := ""
		if i == 0 && caption != "" {
			fileCaption = caption
		}

		ext := strings.ToLower(filepath.Ext(filePath))

		switch {
		case ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp":
			params := tu.Photo(tu.ID(chatID), tu.File(f))
			params.Caption = fileCaption
			_, err = c.bot.SendPhoto(ctx, params)

		case ext == ".mp4" || ext == ".mov" || ext == ".avi" || ext == ".mkv":
			params := tu.Video(tu.ID(chatID), tu.File(f))
			params.Caption = fileCaption
			_, err = c.bot.SendVideo(ctx, params)

		case ext == ".mp3" || ext == ".ogg" || ext == ".wav" || ext == ".m4a" || ext == ".flac":
			params := tu.Audio(tu.ID(chatID), tu.File(f))
			params.Caption = fileCaption
			_, err = c.bot.SendAudio(ctx, params)

		default:
			params := tu.Document(tu.ID(chatID), tu.File(f))
			params.Caption = fileCaption
			_, err = c.bot.SendDocument(ctx, params)
		}

		f.Close()

		if err != nil {
			logger.ErrorCF("telegram", "Failed to send file", map[string]interface{}{
				"path":  filePath,
				"error": err.Error(),
			})
			return fmt.Errorf("failed to send file %s: %w", filepath.Base(filePath), err)
		}

		logger.InfoCF("telegram", "File sent successfully", map[string]interface{}{
			"path": filePath,
		})
	}

	return nil
}

func (c *TelegramChannel) handleMessage(ctx context.Context, update telego.Update) {
	message := update.Message
	if message == nil {
		return
	}

	user := message.From
	if user == nil {
		return
	}

	userID := fmt.Sprintf("%d", user.ID)
	senderID := userID
	if user.Username != "" {
		senderID = fmt.Sprintf("%s|%s", userID, user.Username)
	}

	// Check allowlist to avoid downloading attachments for denied users
	if !c.IsAllowed(userID) && !c.IsAllowed(senderID) {
		logger.DebugCF("telegram", "Message rejected by allowlist", map[string]interface{}{
			"user_id":  userID,
			"username": user.Username,
		})
		return
	}

	chatID := message.Chat.ID
	c.chatIDs[senderID] = chatID

	content := ""
	mediaPaths := []string{}
	attachmentIDs := []string{}
	attachmentMarkers := []string{}
	localFiles := []string{} // Track local files that need cleanup

	// Ensure temp files are cleaned up on return
	defer func() {
		for _, file := range localFiles {
			if err := os.Remove(file); err != nil {
				logger.DebugCF("telegram", "Failed to cleanup temp file", map[string]interface{}{
					"file":  file,
					"error": err.Error(),
				})
			}
		}
	}()
	if message.Text != "" {
		content += message.Text
	}

	if message.Caption != "" {
		if content != "" {
			content += "\n"
		}
		content += message.Caption
	}

	saveAttachment := func(localPath, originalName, mimeType, kind string, persist bool) {
		info, err := os.Stat(localPath)
		if err != nil {
			logger.ErrorCF("telegram", "Failed to stat downloaded attachment", map[string]interface{}{
				"path":  localPath,
				"error": err.Error(),
			})
			return
		}
		if info.Size() > telegramAttachmentMaxBytes {
			attachmentMarkers = append(attachmentMarkers, fmt.Sprintf(
				"[attachment_rejected reason=size_limit name=%s size=%d limit=%d]",
				utils.SanitizeFilename(originalName),
				info.Size(),
				telegramAttachmentMaxBytes,
			))
			c.notifyAttachmentStatus(ctx, chatID, fmt.Sprintf("Attachment rejected (over 100 MB): %s", utils.SanitizeFilename(originalName)))
			return
		}
		if !persist {
			attachmentMarkers = append(attachmentMarkers, fmt.Sprintf(
				"[attachment_notice kind=%s name=%s size=%d note=transcribed_only]",
				kind,
				utils.SanitizeFilename(originalName),
				info.Size(),
			))
			return
		}

		rec, err := c.attachmentStore.SaveFromLocalFile(
			"telegram",
			fmt.Sprintf("%d", chatID),
			fmt.Sprintf("%d", user.ID),
			fmt.Sprintf("%d", message.MessageID),
			originalName,
			mimeType,
			kind,
			localPath,
		)
		if err != nil {
			logger.ErrorCF("telegram", "Failed to persist attachment", map[string]interface{}{
				"path":  localPath,
				"name":  originalName,
				"error": err.Error(),
			})
			attachmentMarkers = append(attachmentMarkers, fmt.Sprintf(
				"[attachment_store_failed name=%s kind=%s]",
				utils.SanitizeFilename(originalName),
				kind,
			))
			return
		}

		attachmentIDs = append(attachmentIDs, rec.ID)
		attachmentMarkers = append(attachmentMarkers, fmt.Sprintf(
			"[attachment_saved id=%s name=%s size=%d path=%s mime=%s kind=%s]",
			rec.ID,
			rec.Name,
			rec.SizeBytes,
			rec.StoredPath,
			rec.MIMEType,
			rec.Kind,
		))
		c.notifyAttachmentStatus(ctx, chatID, fmt.Sprintf(
			"Saved attachment `%s` (%s, %d bytes)\nID: `%s`\nPath: `%s`\nNote: content is not auto-read; use import_attachment to bring it into workspace.",
			rec.Name,
			rec.MIMEType,
			rec.SizeBytes,
			rec.ID,
			rec.StoredPath,
		))
	}

	if message.Photo != nil && len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		photoPath := c.downloadPhoto(ctx, photo.FileID)
		if photoPath != "" {
			saveAttachment(photoPath, fmt.Sprintf("photo_%s.jpg", photo.FileID), "image/jpeg", "photo", true)
			// Don't add to localFiles — agent cleanup handles image removal after encoding
			mediaPaths = append(mediaPaths, photoPath)
			if content != "" {
				content += "\n"
			}
			content += fmt.Sprintf("[image: photo]")
		}
	}

	if message.Voice != nil {
		voicePath := c.downloadFile(ctx, message.Voice.FileID, ".ogg")
		if voicePath != "" {
			localFiles = append(localFiles, voicePath)
			saveAttachment(voicePath, fmt.Sprintf("voice_%s.ogg", message.Voice.FileID), "audio/ogg", "voice", false)

			transcribedText := ""
			if c.transcriber != nil && c.transcriber.IsAvailable() {
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()

				result, err := c.transcriber.Transcribe(ctx, voicePath)
				if err != nil {
					logger.ErrorCF("telegram", "Voice transcription failed", map[string]interface{}{
						"error": err.Error(),
						"path":  voicePath,
					})
					transcribedText = fmt.Sprintf("[voice (transcription failed)]")
				} else {
					transcribedText = fmt.Sprintf("[voice transcription: %s]", result.Text)
					logger.InfoCF("telegram", "Voice transcribed successfully", map[string]interface{}{
						"text": result.Text,
					})
				}
			} else {
				transcribedText = fmt.Sprintf("[voice]")
			}

			if content != "" {
				content += "\n"
			}
			content += transcribedText
		}
	}

	if message.Audio != nil {
		audioPath := c.downloadFile(ctx, message.Audio.FileID, ".mp3")
		if audioPath != "" {
			localFiles = append(localFiles, audioPath)
			audioName := message.Audio.FileName
			if audioName == "" {
				audioName = fmt.Sprintf("audio_%s.mp3", message.Audio.FileID)
			}
			saveAttachment(audioPath, audioName, message.Audio.MimeType, "audio", true)
			if content != "" {
				content += "\n"
			}
			content += fmt.Sprintf("[audio]")
		}
	}

	if message.Document != nil {
		docPath := c.downloadFile(ctx, message.Document.FileID, "")
		if docPath != "" {
			localFiles = append(localFiles, docPath)
			docName := message.Document.FileName
			if docName == "" {
				docName = fmt.Sprintf("document_%s", message.Document.FileID)
			}
			saveAttachment(docPath, docName, message.Document.MimeType, "document", true)
			if content != "" {
				content += "\n"
			}
			content += fmt.Sprintf("[file]")
		}
	}

	if message.ReplyToMessage != nil {
		replyContext := formatTelegramReplyContext(message.ReplyToMessage)
		if replyContext != "" {
			if content != "" {
				content += "\n"
			}
			content += replyContext
		}
	}

	if len(attachmentMarkers) > 0 {
		if content != "" {
			content += "\n"
		}
		content += strings.Join(attachmentMarkers, "\n")
	}

	if content == "" {
		content = "[empty message]"
	}

	logger.DebugCF("telegram", "Received message", map[string]interface{}{
		"sender_id": senderID,
		"chat_id":   fmt.Sprintf("%d", chatID),
		"preview":   utils.Truncate(content, 50),
	})

	// Thinking indicator
	err := c.bot.SendChatAction(ctx, tu.ChatAction(tu.ID(chatID), telego.ChatActionTyping))
	if err != nil {
		logger.ErrorCF("telegram", "Failed to send chat action", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// Stop any previous thinking animation
	chatIDStr := fmt.Sprintf("%d", chatID)
	if prevStop, ok := c.stopThinking.Load(chatIDStr); ok {
		if cf, ok := prevStop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
	}

	// Create cancel function for thinking state
	_, thinkCancel := context.WithTimeout(ctx, 5*time.Minute)
	c.stopThinking.Store(chatIDStr, &thinkingCancel{fn: thinkCancel})

	pMsg, err := c.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), "Thinking... 💭"))
	if err == nil {
		pID := pMsg.MessageID
		c.placeholders.Store(chatIDStr, pID)
	}

	metadata := map[string]string{
		"message_id": fmt.Sprintf("%d", message.MessageID),
		"user_id":    fmt.Sprintf("%d", user.ID),
		"username":   user.Username,
		"first_name": user.FirstName,
		"is_group":   fmt.Sprintf("%t", message.Chat.Type != "private"),
	}
	if len(attachmentIDs) > 0 {
		metadata["attachment_ids"] = strings.Join(attachmentIDs, ",")
	}

	c.HandleMessage(senderID, fmt.Sprintf("%d", chatID), content, mediaPaths, metadata)
}

func (c *TelegramChannel) downloadPhoto(ctx context.Context, fileID string) string {
	file, err := c.bot.GetFile(ctx, &telego.GetFileParams{FileID: fileID})
	if err != nil {
		logger.ErrorCF("telegram", "Failed to get photo file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	return c.downloadFileWithInfo(file, ".jpg")
}

func (c *TelegramChannel) downloadFileWithInfo(file *telego.File, ext string) string {
	if file.FilePath == "" {
		return ""
	}

	url := c.bot.FileDownloadURL(file.FilePath)
	logger.DebugCF("telegram", "File URL", map[string]interface{}{"url": url})

	// Use FilePath as filename; only append ext if FilePath has no extension
	filename := file.FilePath
	if filepath.Ext(filename) == "" {
		filename += ext
	}
	return utils.DownloadFile(url, filename, utils.DownloadOptions{
		LoggerPrefix: "telegram",
	})
}

func (c *TelegramChannel) downloadFile(ctx context.Context, fileID, ext string) string {
	file, err := c.bot.GetFile(ctx, &telego.GetFileParams{FileID: fileID})
	if err != nil {
		logger.ErrorCF("telegram", "Failed to get file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	return c.downloadFileWithInfo(file, ext)
}

func (c *TelegramChannel) notifyAttachmentStatus(ctx context.Context, chatID int64, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if _, err := c.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), text)); err != nil {
		logger.WarnCF("telegram", "Failed to send attachment status message", map[string]interface{}{
			"chat_id": chatID,
			"error":   err.Error(),
		})
	}
}

func parseChatID(chatIDStr string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(chatIDStr, "%d", &id)
	return id, err
}

// splitLargeMessage splits a message into chunks if it exceeds Telegram's limit
func splitLargeMessage(content string, maxLen int) []string {
	if len(content) <= maxLen {
		return []string{content}
	}

	var chunks []string
	remaining := content

	for len(remaining) > 0 {
		chunkSize := maxLen
		if len(remaining) < chunkSize {
			chunkSize = len(remaining)
		}

		// Try to break at a newline near the limit
		if chunkSize == maxLen {
			lastNewline := strings.LastIndex(remaining[:chunkSize], "\n")
			if lastNewline > maxLen*2/3 { // Only if newline is in the last third
				chunkSize = lastNewline + 1
			}
		}

		chunks = append(chunks, remaining[:chunkSize])
		remaining = remaining[chunkSize:]
	}

	return chunks
}

func markdownToTelegramHTML(text string) string {
	if text == "" {
		return ""
	}

	codeBlocks := extractCodeBlocks(text)
	text = codeBlocks.text

	inlineCodes := extractInlineCodes(text)
	text = inlineCodes.text

	text = regexp.MustCompile(`^#{1,6}\s+(.+)$`).ReplaceAllString(text, "$1")

	text = regexp.MustCompile(`^>\s*(.*)$`).ReplaceAllString(text, "$1")

	text = escapeHTML(text)

	text = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllString(text, `<a href="$2">$1</a>`)

	text = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(text, "<b>$1</b>")

	text = regexp.MustCompile(`__(.+?)__`).ReplaceAllString(text, "<b>$1</b>")

	reItalic := regexp.MustCompile(`_([^_]+)_`)
	text = reItalic.ReplaceAllStringFunc(text, func(s string) string {
		match := reItalic.FindStringSubmatch(s)
		if len(match) < 2 {
			return s
		}
		return "<i>" + match[1] + "</i>"
	})

	text = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllString(text, "<s>$1</s>")

	text = regexp.MustCompile(`^[-*]\s+`).ReplaceAllString(text, "• ")

	for i, code := range inlineCodes.codes {
		escaped := escapeHTML(code)
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00IC%d\x00", i), fmt.Sprintf("<code>%s</code>", escaped))
	}

	for i, code := range codeBlocks.codes {
		escaped := escapeHTML(code)
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00CB%d\x00", i), fmt.Sprintf("<pre><code>%s</code></pre>", escaped))
	}

	return text
}

type codeBlockMatch struct {
	text  string
	codes []string
}

func extractCodeBlocks(text string) codeBlockMatch {
	re := regexp.MustCompile("```[\\w]*\\n?([\\s\\S]*?)```")
	matches := re.FindAllStringSubmatch(text, -1)

	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}

	i := 0
	text = re.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("\x00CB%d\x00", i)
		i++
		return placeholder
	})

	return codeBlockMatch{text: text, codes: codes}
}

type inlineCodeMatch struct {
	text  string
	codes []string
}

func extractInlineCodes(text string) inlineCodeMatch {
	re := regexp.MustCompile("`([^`]+)`")
	matches := re.FindAllStringSubmatch(text, -1)

	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}

	i := 0
	text = re.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("\x00IC%d\x00", i)
		i++
		return placeholder
	})

	return inlineCodeMatch{text: text, codes: codes}
}

func escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}

func formatTelegramReplyContext(reply *telego.Message) string {
	if reply == nil {
		return ""
	}

	replyFrom := "unknown"
	if reply.From != nil {
		switch {
		case reply.From.Username != "":
			replyFrom = "@" + reply.From.Username
		case reply.From.FirstName != "":
			replyFrom = reply.From.FirstName
		default:
			replyFrom = fmt.Sprintf("user_%d", reply.From.ID)
		}
	}

	parts := make([]string, 0, 4)
	if reply.Text != "" {
		parts = append(parts, reply.Text)
	}
	if reply.Caption != "" {
		parts = append(parts, reply.Caption)
	}
	if reply.Photo != nil && len(reply.Photo) > 0 {
		parts = append(parts, "[image]")
	}
	if reply.Voice != nil {
		parts = append(parts, "[voice]")
	}
	if reply.Audio != nil {
		parts = append(parts, "[audio]")
	}
	if reply.Document != nil {
		parts = append(parts, "[file]")
	}
	if len(parts) == 0 {
		parts = append(parts, "[non-text message]")
	}

	replyBody := utils.Truncate(strings.Join(parts, " "), 600)
	return fmt.Sprintf("[reply_to from=%s id=%d] %s", replyFrom, reply.MessageID, replyBody)
}
