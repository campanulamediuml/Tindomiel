package httpapi

import (
	"log/slog"
	"net/http"
)

func (s *Server) sessionCwd(w http.ResponseWriter, r *http.Request) {
	found := s.listSession(w, r)
	if found == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": found.Cwd()})
}

func (s *Server) sftpRealpath(w http.ResponseWriter, r *http.Request) {
	client, ok := s.sftpClient(w, r)
	if !ok {
		return
	}
	resolved, err := client.RealPath(requestedPath(r))
	if err != nil {
		slog.Warn("sftp_realpath_failed", "path", requestedPath(r), "error", err)
		writeError(w, http.StatusBadGateway, "SFTP_REALPATH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": resolved})
}

func (s *Server) sftpOpen(w http.ResponseWriter, r *http.Request) {
	found := s.listSession(w, r)
	if found == nil {
		return
	}
	var request struct {
		Path        string `json:"path"`
		Application string `json:"application"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	application := request.Application
	var err error
	if application == "" {
		application, err = s.profiles.EditorApplication()
	} else {
		err = s.profiles.SetEditorApplication(application)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "EDITOR_PREFERENCE_FAILED", err.Error())
		return
	}
	cachePath, err := found.OpenForEdit(request.Path, application)
	if err != nil {
		writeError(w, http.StatusBadGateway, "SFTP_OPEN_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"cachePath": cachePath, "application": application})
}
