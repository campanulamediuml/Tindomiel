package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func (s *Server) recordFrontendLog(w http.ResponseWriter, r *http.Request) {
	var entry struct {
		Message string `json:"message"`
		Level   string `json:"level"`
	}
	if err := decodeJSON(r, &entry); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	entry.Message = strings.TrimSpace(entry.Message)
	if entry.Message == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "log message is required")
		return
	}
	level := slog.LevelInfo
	switch entry.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	s.frontendLogger.Log(r.Context(), level, "frontend_event", "message", entry.Message)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) recentLogs(w http.ResponseWriter, r *http.Request) {
	writeRecentLogs(w, r, s.logPath)
}

func (s *Server) recentFrontendLogs(w http.ResponseWriter, r *http.Request) {
	writeRecentLogs(w, r, s.frontendLogPath)
}

func writeRecentLogs(w http.ResponseWriter, r *http.Request, path string) {
	lineCount := 200
	if value := r.URL.Query().Get("lines"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 && parsed <= 1000 {
			lineCount = parsed
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, 200, map[string]string{"log": "", "path": path})
		return
	}
	if err != nil {
		writeError(w, 500, "LOG_READ_FAILED", err.Error())
		return
	}
	if len(data) > 1024*1024 {
		data = data[len(data)-1024*1024:]
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > lineCount {
		lines = lines[len(lines)-lineCount:]
	}
	writeJSON(w, 200, map[string]string{"log": strings.Join(lines, "\n"), "path": path})
}
