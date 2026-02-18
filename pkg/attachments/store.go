package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type Record struct {
	ID           string    `json:"id"`
	Channel      string    `json:"channel"`
	ChatID       string    `json:"chat_id"`
	UserID       string    `json:"user_id"`
	MessageID    string    `json:"message_id"`
	Name         string    `json:"name"`
	StoredPath   string    `json:"stored_path"`
	MIMEType     string    `json:"mime_type,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256       string    `json:"sha256"`
	CreatedAt    time.Time `json:"created_at"`
	ImportedPath string    `json:"imported_path,omitempty"`
}

type stateFile struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

type Store struct {
	mu        sync.RWMutex
	statePath string
	rootPath  string
	records   map[string]Record
}

func NewStore(workspace string) *Store {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".picoclaw", "attachments")
	statePath := filepath.Join(workspace, "state", "attachments.json")

	_ = os.MkdirAll(filepath.Dir(statePath), 0755)
	_ = os.MkdirAll(root, 0755)

	s := &Store{
		statePath: statePath,
		rootPath:  root,
		records:   map[string]Record{},
	}
	_ = s.load()
	return s
}

func (s *Store) RootPath() string {
	return s.rootPath
}

func (s *Store) SaveFromLocalFile(channel, chatID, userID, messageID, originalName, mimeType, kind, localPath string) (Record, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return Record{}, fmt.Errorf("stat local file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Record{}, fmt.Errorf("local path is not a regular file: %s", localPath)
	}

	now := time.Now().UTC()
	dayPath := filepath.Join(
		s.rootPath,
		strings.ToLower(strings.TrimSpace(channel)),
		strings.TrimSpace(chatID),
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
	)
	if err := os.MkdirAll(dayPath, 0755); err != nil {
		return Record{}, fmt.Errorf("mkdir attachment day path: %w", err)
	}

	baseName := utils.SanitizeFilename(originalName)
	if baseName == "" {
		baseName = filepath.Base(localPath)
	}
	destName := fmt.Sprintf("%s_%s_%s", now.Format("150405"), uuid.NewString()[:8], baseName)
	destPath := filepath.Join(dayPath, destName)

	size, sum, err := copyWithHash(localPath, destPath)
	if err != nil {
		return Record{}, err
	}

	rec := Record{
		ID:         "att_" + uuid.NewString(),
		Channel:    channel,
		ChatID:     chatID,
		UserID:     userID,
		MessageID:  messageID,
		Name:       baseName,
		StoredPath: destPath,
		MIMEType:   mimeType,
		Kind:       kind,
		SizeBytes:  size,
		SHA256:     sum,
		CreatedAt:  now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.ID] = rec
	if err := s.saveLocked(); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func (s *Store) GetByID(id string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[id]
	return r, ok
}

func (s *Store) MarkImported(id, importedPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return fmt.Errorf("attachment not found: %s", id)
	}
	r.ImportedPath = importedPath
	s.records[id] = r
	return s.saveLocked()
}

func (s *Store) IsInRoot(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	root, err := filepath.Abs(s.rootPath)
	if err != nil {
		return false
	}
	return strings.HasPrefix(abs, root)
}

func copyWithHash(srcPath, dstPath string) (int64, string, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return 0, "", fmt.Errorf("open source file: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return 0, "", fmt.Errorf("create destination file: %w", err)
	}
	defer dst.Close()

	hasher := sha256.New()
	w := io.MultiWriter(dst, hasher)
	n, err := io.Copy(w, src)
	if err != nil {
		_ = os.Remove(dstPath)
		return 0, "", fmt.Errorf("copy file: %w", err)
	}
	return n, hex.EncodeToString(hasher.Sum(nil)), nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var st stateFile
	if err := json.Unmarshal(data, &st); err != nil {
		s.records = map[string]Record{}
		return nil
	}
	out := make(map[string]Record, len(st.Records))
	for _, r := range st.Records {
		out[r.ID] = r
	}
	s.records = out
	return nil
}

func (s *Store) saveLocked() error {
	records := make([]Record, 0, len(s.records))
	for _, r := range s.records {
		records = append(records, r)
	}

	st := stateFile{
		Version: 1,
		Records: records,
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal attachment store: %w", err)
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write attachment temp: %w", err)
	}
	if err := os.Rename(tmp, s.statePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace attachment state: %w", err)
	}
	return nil
}
