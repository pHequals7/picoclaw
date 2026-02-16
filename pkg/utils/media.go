package utils

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// MediaImage holds a base64-encoded image with its MIME type.
type MediaImage struct {
	MimeType   string
	Base64Data string
}

// IsImageFile checks if a file path has an image extension.
func IsImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

// DetectImageMimeType returns the MIME type for an image file based on extension.
func DetectImageMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// LoadAndEncodeImage reads an image file and returns its MIME type and base64-encoded data.
func LoadAndEncodeImage(path string) (mimeType, base64Data string, err error) {
	mimeType = DetectImageMimeType(path)
	if mimeType == "" {
		return "", "", fmt.Errorf("unsupported image type: %s", filepath.Ext(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("reading image %s: %w", path, err)
	}
	base64Data = base64.StdEncoding.EncodeToString(data)
	return mimeType, base64Data, nil
}

// ProcessMediaImages filters media paths to images and encodes each one.
func ProcessMediaImages(paths []string) []MediaImage {
	var images []MediaImage
	for _, p := range paths {
		if !IsImageFile(p) {
			continue
		}
		mimeType, b64, err := LoadAndEncodeImage(p)
		if err != nil {
			logger.WarnCF("media", "Failed to encode image",
				map[string]interface{}{"path": p, "error": err.Error()})
			continue
		}
		images = append(images, MediaImage{MimeType: mimeType, Base64Data: b64})
		logger.DebugCF("media", "Encoded image for LLM",
			map[string]interface{}{"path": p, "mime": mimeType, "size_bytes": len(b64) * 3 / 4})
	}
	return images
}

// IsAudioFile checks if a file is an audio file based on its filename extension and content type.
func IsAudioFile(filename, contentType string) bool {
	audioExtensions := []string{".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac", ".wma"}
	audioTypes := []string{"audio/", "application/ogg", "application/x-ogg"}

	for _, ext := range audioExtensions {
		if strings.HasSuffix(strings.ToLower(filename), ext) {
			return true
		}
	}

	for _, audioType := range audioTypes {
		if strings.HasPrefix(strings.ToLower(contentType), audioType) {
			return true
		}
	}

	return false
}

// SanitizeFilename removes potentially dangerous characters from a filename
// and returns a safe version for local filesystem storage.
func SanitizeFilename(filename string) string {
	// Get the base filename without path
	base := filepath.Base(filename)

	// Remove any directory traversal attempts
	base = strings.ReplaceAll(base, "..", "")
	base = strings.ReplaceAll(base, "/", "_")
	base = strings.ReplaceAll(base, "\\", "_")

	return base
}

// DownloadOptions holds optional parameters for downloading files
type DownloadOptions struct {
	Timeout      time.Duration
	ExtraHeaders map[string]string
	LoggerPrefix string
}

// DownloadFile downloads a file from URL to a local temp directory.
// Returns the local file path or empty string on error.
func DownloadFile(url, filename string, opts DownloadOptions) string {
	// Set defaults
	if opts.Timeout == 0 {
		opts.Timeout = 60 * time.Second
	}
	if opts.LoggerPrefix == "" {
		opts.LoggerPrefix = "utils"
	}

	mediaDir := filepath.Join(os.TempDir(), "picoclaw_media")
	if err := os.MkdirAll(mediaDir, 0700); err != nil {
		logger.ErrorCF(opts.LoggerPrefix, "Failed to create media directory", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	// Generate unique filename with UUID prefix to prevent conflicts
	ext := filepath.Ext(filename)
	safeName := SanitizeFilename(filename)
	localPath := filepath.Join(mediaDir, uuid.New().String()[:8]+"_"+safeName+ext)

	// Create HTTP request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		logger.ErrorCF(opts.LoggerPrefix, "Failed to create download request", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	// Add extra headers (e.g., Authorization for Slack)
	for key, value := range opts.ExtraHeaders {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: opts.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.ErrorCF(opts.LoggerPrefix, "Failed to download file", map[string]interface{}{
			"error": err.Error(),
			"url":   url,
		})
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.ErrorCF(opts.LoggerPrefix, "File download returned non-200 status", map[string]interface{}{
			"status": resp.StatusCode,
			"url":    url,
		})
		return ""
	}

	out, err := os.Create(localPath)
	if err != nil {
		logger.ErrorCF(opts.LoggerPrefix, "Failed to create local file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(localPath)
		logger.ErrorCF(opts.LoggerPrefix, "Failed to write file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	logger.DebugCF(opts.LoggerPrefix, "File downloaded successfully", map[string]interface{}{
		"path": localPath,
	})

	return localPath
}

// DownloadFileSimple is a simplified version of DownloadFile without options
func DownloadFileSimple(url, filename string) string {
	return DownloadFile(url, filename, DownloadOptions{
		LoggerPrefix: "media",
	})
}
