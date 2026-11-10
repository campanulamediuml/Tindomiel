package httpapi

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func (s *Server) recentLogs(w http.ResponseWriter, r *http.Request) {
	lineCount := 200
	if value := r.URL.Query().Get("lines"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 && parsed <= 1000 {
			lineCount = parsed
		}
	}
	data, err := os.ReadFile(s.logPath)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, 200, map[string]string{"log": "", "path": s.logPath})
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
	writeJSON(w, 200, map[string]string{"log": strings.Join(lines, "\n"), "path": s.logPath})
}
