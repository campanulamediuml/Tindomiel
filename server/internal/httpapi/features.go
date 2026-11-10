package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"tindomiel/internal/configstore"
)

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	var profile configstore.Profile
	if err := decodeJSON(r, &profile); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	profile.ID = r.PathValue("id")
	updated, err := s.profiles.Update(profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PROFILE_UPDATE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, publicProfile(updated))
}

func (s *Server) listFolders(w http.ResponseWriter, _ *http.Request) {
	folders, err := s.profiles.ListFolders()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FOLDERS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, folders)
}

func (s *Server) createFolder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	folder, err := s.profiles.CreateFolder(request.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "FOLDER_INVALID", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, folder)
}

func (s *Server) resourceMetrics(w http.ResponseWriter, r *http.Request) {
	found := s.listSession(w, r)
	if found == nil {
		return
	}
	if metrics, ok := found.CachedMetrics(); ok {
		writeJSON(w, http.StatusOK, metrics)
		return
	}
	metrics, err := found.Metrics(r.Context())
	if err != nil {
		slog.Warn("metrics_failed", "sessionId", found.ID(), "error", err)
		writeError(w, http.StatusBadGateway, "METRICS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) chooseFile(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "osascript", "-e", `POSIX path of (choose file with prompt "选择私钥")`)
	output, err := command.Output()
	if err != nil {
		writeError(w, http.StatusConflict, "FILE_SELECTION_CANCELLED", "没有选择文件")
		return
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		writeError(w, http.StatusBadRequest, "FILE_SELECTION_EMPTY", "文件路径为空")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *Server) chooseApplication(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	script := `POSIX path of (choose file with prompt "选择用于打开文件的应用" default location (POSIX file "/Applications") of type {"app"})`
	output, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		writeError(w, http.StatusConflict, "FILE_SELECTION_CANCELLED", "没有选择应用")
		return
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		writeError(w, http.StatusBadRequest, "FILE_SELECTION_EMPTY", "应用路径为空")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func publicProfile(profile configstore.Profile) configstore.Profile {
	profile.Options.Password = ""
	profile.Options.PrivateKeyPassphrase = ""
	return profile
}
