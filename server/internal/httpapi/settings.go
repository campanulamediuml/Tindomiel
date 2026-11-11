package httpapi

import (
	"net/http"

	"tindomiel/internal/configstore"
)

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	settings, err := s.profiles.AppSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SETTINGS_FAILED", err.Error())
		return
	}
	settings.DownloadDirectory, err = s.defaultDownloadDirectory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SETTINGS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var settings configstore.AppSettings
	if err := decodeJSON(r, &settings); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if err := s.profiles.SetAppSettings(settings); err != nil {
		writeError(w, http.StatusBadRequest, "SETTINGS_INVALID", err.Error())
		return
	}
	s.getSettings(w, r)
}
