package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Server) defaultDownloadPath(w http.ResponseWriter, _ *http.Request) {
	path, err := s.defaultDownloadDirectory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_PATH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *Server) openDownloadDirectory(w http.ResponseWriter, _ *http.Request) {
	path, err := s.defaultDownloadDirectory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_PATH_FAILED", err.Error())
		return
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_PATH_FAILED", err.Error())
		return
	}
	command := exec.Command("open", path)
	if err := command.Start(); err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_OPEN_FAILED", err.Error())
		return
	}
	if err := command.Process.Release(); err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_OPEN_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) chooseDownloadDirectory(w http.ResponseWriter, r *http.Request) {
	current, err := s.defaultDownloadDirectory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_PATH_FAILED", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	script := fmt.Sprintf(`POSIX path of (choose folder with prompt "选择下载目录" default location (POSIX file %s))`, strconv.Quote(current))
	output, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		writeError(w, http.StatusConflict, "DIRECTORY_SELECTION_CANCELLED", "没有选择文件夹")
		return
	}
	selected := strings.TrimSpace(string(output))
	if selected == "" || !filepath.IsAbs(selected) {
		writeError(w, http.StatusBadRequest, "DIRECTORY_SELECTION_EMPTY", "文件夹路径无效")
		return
	}
	selected = filepath.Clean(selected)
	if err := s.profiles.SetDownloadDirectory(selected); err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_PREFERENCE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": selected})
}

func (s *Server) defaultDownloadDirectory() (string, error) {
	if path := os.Getenv("TINDOMIEL_DOWNLOADS"); path != "" {
		return path, nil
	}
	preferred, err := s.profiles.DownloadDirectory()
	if err != nil {
		return "", err
	}
	if preferred != "" {
		return preferred, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Downloads"), nil
}
