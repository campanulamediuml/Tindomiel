package httpapi

import (
	"net/http"

	"tindomiel/internal/sshconn"
)

func (s *Server) defaultKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, sshconn.DefaultPrivateKeys())
}

func (s *Server) inspectKey(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	info, err := sshconn.InspectPrivateKey(request.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "KEY_INVALID", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}
