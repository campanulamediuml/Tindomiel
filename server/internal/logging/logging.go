package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxFileSize  = 4 * 1024 * 1024
	retentionAge = 72 * time.Hour
)

func Setup(path, levelText string) (*slog.Logger, func() error, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	file, err := newRotateWriter(path)
	if err != nil {
		return nil, nil, err
	}
	level := slog.LevelInfo
	switch strings.ToLower(levelText) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	handler := slog.NewJSONHandler(io.MultiWriter(os.Stderr, file), &slog.HandlerOptions{Level: level})
	return slog.New(handler), file.Close, nil
}

type rotateWriter struct {
	mu          sync.Mutex
	path        string
	file        *os.File
	size        int64
	lastCleanup time.Time
}

func newRotateWriter(path string) (*rotateWriter, error) {
	writer := &rotateWriter{path: path, lastCleanup: time.Now()}
	if err := writer.open(); err != nil {
		return nil, err
	}
	if err := writer.cleanup(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *rotateWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(data)) > maxFileSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	count, err := w.file.Write(data)
	w.size += int64(count)
	if time.Since(w.lastCleanup) > time.Hour {
		w.lastCleanup = time.Now()
		if err := w.cleanup(); err != nil {
			return count, err
		}
	}
	return count, err
}

func (w *rotateWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotateWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	backup := w.path + "." + time.Now().Format("20060102")
	if _, err := os.Stat(backup); err == nil {
		backup = w.path + "." + time.Now().Format("20060102-150405")
	}
	if err := os.Rename(w.path, backup); err != nil && !os.IsNotExist(err) {
		return err
	}
	return w.open()
}

func (w *rotateWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *rotateWriter) cleanup() error {
	entries, err := os.ReadDir(filepath.Dir(w.path))
	if err != nil {
		return err
	}
	prefix := filepath.Base(w.path) + "."
	cutoff := time.Now().Add(-retentionAge)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(filepath.Dir(w.path), entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
