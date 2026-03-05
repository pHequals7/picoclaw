package media

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// MediaMeta describes a stored media asset.
type MediaMeta struct {
	OriginalName string
	MIMEType     string
	SizeBytes    int64
	Scope        string // "turn", "session", "persistent"
}

// MediaStore manages media assets referenced by media:// URIs.
type MediaStore interface {
	// Store adds a local file and returns a "media://<uuid>" reference.
	Store(localPath string, meta MediaMeta) (string, error)
	// Resolve returns the local path for a media:// reference.
	Resolve(ref string) (string, error)
	// ResolveWithMeta returns the local path and metadata.
	ResolveWithMeta(ref string) (string, MediaMeta, error)
	// ReleaseScope removes all entries with the given scope.
	ReleaseScope(scope string)
}

type mediaEntry struct {
	localPath string
	meta      MediaMeta
}

// FileMediaStore is an in-memory map-backed MediaStore using UUID refs.
type FileMediaStore struct {
	mu      sync.RWMutex
	entries map[string]mediaEntry // key = UUID portion of media://uuid
}

// NewFileMediaStore creates a new FileMediaStore.
func NewFileMediaStore() *FileMediaStore {
	return &FileMediaStore{
		entries: make(map[string]mediaEntry),
	}
}

func (s *FileMediaStore) Store(localPath string, meta MediaMeta) (string, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", localPath, err)
	}
	if meta.SizeBytes == 0 {
		meta.SizeBytes = info.Size()
	}

	id := uuid.New().String()
	s.mu.Lock()
	s.entries[id] = mediaEntry{localPath: localPath, meta: meta}
	s.mu.Unlock()
	return "media://" + id, nil
}

func (s *FileMediaStore) Resolve(ref string) (string, error) {
	path, _, err := s.ResolveWithMeta(ref)
	return path, err
}

func (s *FileMediaStore) ResolveWithMeta(ref string) (string, MediaMeta, error) {
	id := strings.TrimPrefix(ref, "media://")
	if id == ref {
		return "", MediaMeta{}, fmt.Errorf("invalid media ref: %s", ref)
	}

	s.mu.RLock()
	entry, ok := s.entries[id]
	s.mu.RUnlock()
	if !ok {
		return "", MediaMeta{}, fmt.Errorf("media not found: %s", ref)
	}
	return entry.localPath, entry.meta, nil
}

func (s *FileMediaStore) ReleaseScope(scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, entry := range s.entries {
		if entry.meta.Scope == scope {
			delete(s.entries, id)
		}
	}
}

// StreamEncodeFile reads a file and returns its MIME type and base64 data
// using streaming encoding. Peak memory is ~1.33x file size.
func StreamEncodeFile(path string, maxSize int64) (mimeType, b64 string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("stat %s: %w", path, err)
	}
	if maxSize > 0 && info.Size() > maxSize {
		return "", "", fmt.Errorf("file %s exceeds max size (%d > %d bytes)", path, info.Size(), maxSize)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	// Read first 512 bytes for MIME detection
	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return "", "", fmt.Errorf("read header: %w", err)
	}
	header = header[:n]
	mimeType = http.DetectContentType(header)

	// Seek back to start
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", fmt.Errorf("seek: %w", err)
	}

	// Streaming base64 encode: file → base64.NewEncoder → bytes.Buffer
	var buf bytes.Buffer
	encoder := base64.NewEncoder(base64.StdEncoding, &buf)
	if _, err := io.Copy(encoder, f); err != nil {
		return "", "", fmt.Errorf("encode: %w", err)
	}
	encoder.Close()

	return mimeType, buf.String(), nil
}
