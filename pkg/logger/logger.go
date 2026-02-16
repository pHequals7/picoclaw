package logger

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

var (
	logLevelNames = map[LogLevel]string{
		DEBUG: "DEBUG",
		INFO:  "INFO",
		WARN:  "WARN",
		ERROR: "ERROR",
		FATAL: "FATAL",
	}

	currentLevel = INFO
	logger       *Logger
	once         sync.Once
	mu           sync.RWMutex
)

type Logger struct {
	file             *os.File
	filePath         string
	rotationEnabled  bool
	maxSizeBytes     int64
	maxAgeDays       int
	currentSize      int64
	lastRotationTime time.Time
	rotationMu       sync.Mutex
}

type LogEntry struct {
	Level     string                 `json:"level"`
	Timestamp string                 `json:"timestamp"`
	Component string                 `json:"component,omitempty"`
	Message   string                 `json:"message"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	Caller    string                 `json:"caller,omitempty"`
}

func init() {
	once.Do(func() {
		logger = &Logger{}
	})
}

func SetLevel(level LogLevel) {
	mu.Lock()
	defer mu.Unlock()
	currentLevel = level
}

func GetLevel() LogLevel {
	mu.RLock()
	defer mu.RUnlock()
	return currentLevel
}

func EnableFileLogging(filePath string) error {
	return EnableFileLoggingWithRotation(filePath, false, 0, 0)
}

func EnableFileLoggingWithRotation(filePath string, rotationEnabled bool, maxSizeMB int, maxAgeDays int) error {
	mu.Lock()
	defer mu.Unlock()

	// Expand home directory in path
	if strings.HasPrefix(filePath, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			filePath = filepath.Join(home, filePath[2:])
		}
	}

	// Ensure directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	// Get current file size
	stat, err := file.Stat()
	var currentSize int64
	if err == nil {
		currentSize = stat.Size()
	}

	if logger.file != nil {
		logger.file.Close()
	}

	logger.file = file
	logger.filePath = filePath
	logger.rotationEnabled = rotationEnabled
	logger.maxSizeBytes = int64(maxSizeMB) * 1024 * 1024
	logger.maxAgeDays = maxAgeDays
	logger.currentSize = currentSize
	logger.lastRotationTime = time.Now()

	log.Println("File logging enabled:", filePath)
	if rotationEnabled {
		log.Printf("Log rotation enabled: max_size=%dMB, max_age=%d days", maxSizeMB, maxAgeDays)
	}
	return nil
}

func DisableFileLogging() {
	mu.Lock()
	defer mu.Unlock()

	if logger.file != nil {
		logger.file.Close()
		logger.file = nil
		log.Println("File logging disabled")
	}
}

func (l *Logger) shouldRotate() bool {
	if !l.rotationEnabled {
		return false
	}

	// Check size-based rotation
	if l.maxSizeBytes > 0 && l.currentSize >= l.maxSizeBytes {
		return true
	}

	// Check age-based rotation (daily)
	if l.maxAgeDays > 0 {
		now := time.Now()
		if now.YearDay() != l.lastRotationTime.YearDay() || now.Year() != l.lastRotationTime.Year() {
			return true
		}
	}

	return false
}

func (l *Logger) rotateFile() error {
	l.rotationMu.Lock()
	defer l.rotationMu.Unlock()

	if l.file == nil {
		return nil
	}

	// Close current file
	l.file.Close()

	// Generate rotation timestamp
	timestamp := time.Now().Format("20060102-150405")
	rotatedPath := fmt.Sprintf("%s.%s", l.filePath, timestamp)

	// Rename current file
	if err := os.Rename(l.filePath, rotatedPath); err != nil {
		// If rename fails, try to reopen the original file
		file, openErr := os.OpenFile(l.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if openErr == nil {
			l.file = file
		}
		return fmt.Errorf("failed to rotate log file: %w", err)
	}

	// Open new file
	file, err := os.OpenFile(l.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to create new log file: %w", err)
	}

	l.file = file
	l.currentSize = 0
	l.lastRotationTime = time.Now()

	// Clean up old rotated files
	go l.cleanOldRotatedFiles()

	return nil
}

func (l *Logger) cleanOldRotatedFiles() {
	if l.maxAgeDays <= 0 {
		return
	}

	dir := filepath.Dir(l.filePath)
	baseName := filepath.Base(l.filePath)
	cutoffTime := time.Now().AddDate(0, 0, -l.maxAgeDays)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasPrefix(name, baseName+".") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoffTime) {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

func logMessage(level LogLevel, component string, message string, fields map[string]interface{}) {
	if level < currentLevel {
		return
	}

	entry := LogEntry{
		Level:     logLevelNames[level],
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Component: component,
		Message:   message,
		Fields:    fields,
	}

	if pc, file, line, ok := runtime.Caller(2); ok {
		fn := runtime.FuncForPC(pc)
		if fn != nil {
			entry.Caller = fmt.Sprintf("%s:%d (%s)", file, line, fn.Name())
		}
	}

	if logger.file != nil {
		// Check if rotation is needed
		if logger.shouldRotate() {
			if err := logger.rotateFile(); err != nil {
				log.Printf("Failed to rotate log file: %v", err)
			}
		}

		jsonData, err := json.Marshal(entry)
		if err == nil {
			line := string(jsonData) + "\n"
			n, writeErr := logger.file.WriteString(line)
			if writeErr == nil {
				logger.currentSize += int64(n)
			}
		}
	}

	var fieldStr string
	if len(fields) > 0 {
		fieldStr = " " + formatFields(fields)
	}

	logLine := fmt.Sprintf("[%s] [%s]%s %s%s",
		entry.Timestamp,
		logLevelNames[level],
		formatComponent(component),
		message,
		fieldStr,
	)

	log.Println(logLine)

	if level == FATAL {
		os.Exit(1)
	}
}

func formatComponent(component string) string {
	if component == "" {
		return ""
	}
	return fmt.Sprintf(" %s:", component)
}

func formatFields(fields map[string]interface{}) string {
	var parts []string
	for k, v := range fields {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return fmt.Sprintf("{%s}", strings.Join(parts, ", "))
}

func Debug(message string) {
	logMessage(DEBUG, "", message, nil)
}

func DebugC(component string, message string) {
	logMessage(DEBUG, component, message, nil)
}

func DebugF(message string, fields map[string]interface{}) {
	logMessage(DEBUG, "", message, fields)
}

func DebugCF(component string, message string, fields map[string]interface{}) {
	logMessage(DEBUG, component, message, fields)
}

func Info(message string) {
	logMessage(INFO, "", message, nil)
}

func InfoC(component string, message string) {
	logMessage(INFO, component, message, nil)
}

func InfoF(message string, fields map[string]interface{}) {
	logMessage(INFO, "", message, fields)
}

func InfoCF(component string, message string, fields map[string]interface{}) {
	logMessage(INFO, component, message, fields)
}

func Warn(message string) {
	logMessage(WARN, "", message, nil)
}

func WarnC(component string, message string) {
	logMessage(WARN, component, message, nil)
}

func WarnF(message string, fields map[string]interface{}) {
	logMessage(WARN, "", message, fields)
}

func WarnCF(component string, message string, fields map[string]interface{}) {
	logMessage(WARN, component, message, fields)
}

func Error(message string) {
	logMessage(ERROR, "", message, nil)
}

func ErrorC(component string, message string) {
	logMessage(ERROR, component, message, nil)
}

func ErrorF(message string, fields map[string]interface{}) {
	logMessage(ERROR, "", message, fields)
}

func ErrorCF(component string, message string, fields map[string]interface{}) {
	logMessage(ERROR, component, message, fields)
}

func Fatal(message string) {
	logMessage(FATAL, "", message, nil)
}

func FatalC(component string, message string) {
	logMessage(FATAL, component, message, nil)
}

func FatalF(message string, fields map[string]interface{}) {
	logMessage(FATAL, "", message, fields)
}

func FatalCF(component string, message string, fields map[string]interface{}) {
	logMessage(FATAL, component, message, fields)
}
