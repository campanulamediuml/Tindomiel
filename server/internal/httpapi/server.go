package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tindomiel/internal/configstore"
	"tindomiel/internal/session"
	"tindomiel/internal/sshconn"

	"github.com/coder/websocket"
	"github.com/pkg/sftp"
)

type Server struct {
	token    string
	profiles *configstore.Store
	manager  *session.Manager
	hostKeys *sshconn.HostKeys
	static   http.Handler
	logPath  string

	ticketMu sync.Mutex
	tickets  map[string]ticket
}

type ticket struct {
	sessionID string
	purpose   string
	expiresAt time.Time
}

func New(token, staticDir string, profiles *configstore.Store, manager *session.Manager, hostKeys *sshconn.HostKeys, logPath string) *Server {
	server := &Server{token: token, profiles: profiles, manager: manager, hostKeys: hostKeys, logPath: logPath, tickets: make(map[string]ticket)}
	if staticDir != "" {
		server.static = http.FileServer(http.Dir(staticDir))
	}
	return server
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/health", s.health)
	api.HandleFunc("GET /api/logs/recent", s.recentLogs)
	api.HandleFunc("GET /api/local/download-path", s.defaultDownloadPath)
	api.HandleFunc("POST /api/local/open-downloads", s.openDownloadDirectory)
	api.HandleFunc("POST /api/local/choose-download-directory", s.chooseDownloadDirectory)
	api.HandleFunc("GET /api/profiles", s.listProfiles)
	api.HandleFunc("POST /api/profiles", s.createProfile)
	api.HandleFunc("PUT /api/profiles/{id}", s.updateProfile)
	api.HandleFunc("DELETE /api/profiles/{id}", s.deleteProfile)
	api.HandleFunc("GET /api/folders", s.listFolders)
	api.HandleFunc("POST /api/folders", s.createFolder)
	api.HandleFunc("POST /api/local/choose-file", s.chooseFile)
	api.HandleFunc("POST /api/local/choose-application", s.chooseApplication)
	api.HandleFunc("GET /api/keys/defaults", s.defaultKeys)
	api.HandleFunc("POST /api/keys/info", s.inspectKey)
	api.HandleFunc("GET /api/metrics/{id}", s.resourceMetrics)
	api.HandleFunc("GET /api/sessions", s.listSessions)
	api.HandleFunc("POST /api/sessions", s.createSession)
	api.HandleFunc("GET /api/sessions/{id}/cwd", s.sessionCwd)
	api.HandleFunc("DELETE /api/sessions/{id}", s.deleteSession)
	api.HandleFunc("POST /api/sessions/{id}/reconnect", s.reconnectSession)
	api.HandleFunc("POST /api/sessions/{id}/ws-ticket", s.createWSTicket)
	api.HandleFunc("POST /api/host-keys/accept", s.acceptHostKey)
	api.HandleFunc("GET /api/sftp/{id}/list", s.sftpList)
	api.HandleFunc("GET /api/sftp/{id}/realpath", s.sftpRealpath)
	api.HandleFunc("GET /api/sftp/{id}/download", s.sftpDownload)
	api.HandleFunc("POST /api/sftp/{id}/download-file", s.sftpDownloadFile)
	api.HandleFunc("POST /api/sftp/{id}/download-directory", s.sftpDownloadDirectory)
	api.HandleFunc("POST /api/sftp/{id}/download-ticket", s.sftpDownloadTicket)
	api.HandleFunc("POST /api/sftp/{id}/upload", s.sftpUpload)
	api.HandleFunc("POST /api/sftp/{id}/open", s.sftpOpen)

	root := http.NewServeMux()
	root.Handle("/api/", s.protect(api))
	root.Handle("/ws/terminal", s.protect(http.HandlerFunc(s.terminal)))
	if s.static != nil {
		root.Handle("/", s.static)
	} else {
		root.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "Tindomiel server is running")
		})
	}
	return root
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": "0.1.0"})
}

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.profiles.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PROFILES_FAILED", err.Error())
		return
	}
	for index := range profiles {
		profiles[index] = publicProfile(profiles[index])
	}
	writeJSON(w, http.StatusOK, profiles)
}

func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) {
	var profile configstore.Profile
	if err := decodeJSON(r, &profile); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	created, err := s.profiles.Create(profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PROFILE_INVALID", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, publicProfile(created))
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	if err := s.profiles.Delete(r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, "PROFILE_DELETE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listSessions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.manager.List())
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ProfileID string `json:"profileId"`
		Cols      int    `json:"cols"`
		Rows      int    `json:"rows"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if request.Cols == 0 {
		request.Cols = 120
	}
	if request.Rows == 0 {
		request.Rows = 32
	}
	if request.Cols < 2 || request.Rows < 2 || request.Cols > 1000 || request.Rows > 500 {
		writeError(w, http.StatusBadRequest, "INVALID_TERMINAL_SIZE", "invalid terminal size")
		return
	}
	created, err := s.manager.Create(r.Context(), request.ProfileID, request.Cols, request.Rows)
	if err != nil {
		var hostKeyError *sshconn.HostKeyError
		if errors.As(err, &hostKeyError) {
			var details any
			if hostKeyError.Verification != nil {
				details = hostKeyError.Verification
			}
			writeErrorDetails(w, http.StatusConflict, hostKeyError.Kind, hostKeyError.Error(), details)
			return
		}
		writeError(w, http.StatusBadGateway, "SSH_CONNECT_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, created.Info())
}

func (s *Server) listSession(w http.ResponseWriter, r *http.Request) *session.Session {
	found, err := s.manager.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", err.Error())
		return nil
	}
	return found
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.Delete(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) reconnectSession(w http.ResponseWriter, r *http.Request) {
	found := s.listSession(w, r)
	if found == nil {
		return
	}
	if err := found.Reconnect(); err != nil {
		writeError(w, http.StatusConflict, "RECONNECT_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessionId": found.ID(), "status": session.Reconnecting})
}

func (s *Server) createWSTicket(w http.ResponseWriter, r *http.Request) {
	found := s.listSession(w, r)
	if found == nil {
		return
	}
	value := s.newTicket(found.ID(), "ws")
	writeJSON(w, http.StatusOK, map[string]any{"ticket": value, "expiresAt": time.Now().Add(time.Minute).Unix()})
}

func (s *Server) acceptHostKey(w http.ResponseWriter, r *http.Request) {
	var request struct {
		VerificationID string `json:"verificationId"`
		Fingerprint    string `json:"fingerprint"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if err := s.hostKeys.Accept(request.VerificationID, request.Fingerprint); err != nil {
		writeError(w, http.StatusBadRequest, "HOST_KEY_ACCEPT_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) terminal(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if !s.consumeTicket(r.URL.Query().Get("ticket"), sessionID, "ws") {
		writeError(w, http.StatusUnauthorized, "INVALID_WS_TICKET", "invalid or expired WebSocket ticket")
		return
	}
	found, err := s.manager.Get(sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", err.Error())
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"localhost", "localhost:*", "127.0.0.1", "127.0.0.1:*", "tauri.localhost"}})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "terminal detached")
	conn.SetReadLimit(1024 * 1024)
	if err := found.Attach(conn); err != nil {
		return
	}
	defer found.Detach(conn)
	for {
		messageType, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		switch messageType {
		case websocket.MessageBinary:
			if err := found.WriteInput(data); err != nil {
				_ = found.SendError(err.Error())
			}
		case websocket.MessageText:
			var control struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &control) == nil && control.Type == "resize" {
				if err := found.Resize(control.Cols, control.Rows); err != nil {
					_ = found.SendError(err.Error())
				}
			}
		}
	}
}

type fileEntry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	Mtime       int64  `json:"mtime"`
	Created     int64  `json:"created"`
	Permissions string `json:"permissions"`
}

func (s *Server) sftpClient(w http.ResponseWriter, r *http.Request) (*sftp.Client, bool) {
	found := s.listSession(w, r)
	if found == nil {
		return nil, false
	}
	sshClient, err := found.Client()
	if err != nil {
		writeError(w, http.StatusConflict, "SSH_NOT_READY", err.Error())
		return nil, false
	}
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		writeError(w, http.StatusBadGateway, "SFTP_FAILED", err.Error())
		return nil, false
	}
	return client, true
}

func (s *Server) sftpList(w http.ResponseWriter, r *http.Request) {
	client, ok := s.sftpClient(w, r)
	if !ok {
		return
	}
	defer client.Close()
	remotePath := requestedPath(r)
	entries, err := client.ReadDir(remotePath)
	if err != nil {
		slog.Warn("sftp_list_failed", "path", remotePath, "error", err)
		writeError(w, http.StatusBadGateway, "SFTP_LIST_FAILED", err.Error())
		return
	}
	result := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		kind := "file"
		if entry.IsDir() {
			kind = "directory"
		} else if entry.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
		}
		result = append(result, fileEntry{Name: entry.Name(), Type: kind, Size: entry.Size(), Mtime: entry.ModTime().Unix(), Permissions: entry.Mode().String()})
	}
	if len(result) > 0 {
		found := s.listSession(w, r)
		if found == nil {
			return
		}
		paths := make([]string, len(result))
		for index, entry := range result {
			paths[index] = path.Join(remotePath, entry.Name)
		}
		creationTimes, err := found.FileCreationTimes(paths)
		if err != nil {
			slog.Warn("file_creation_times_failed", "path", remotePath, "error", err)
		} else {
			for index, remoteFilePath := range paths {
				result[index].Created = creationTimes[remoteFilePath]
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if (result[i].Type == "directory") != (result[j].Type == "directory") {
			return result[i].Type == "directory"
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) sftpDownloadTicket(w http.ResponseWriter, r *http.Request) {
	found := s.listSession(w, r)
	if found == nil {
		return
	}
	value := s.newTicket(found.ID(), "download")
	writeJSON(w, http.StatusOK, map[string]any{"ticket": value, "expiresAt": time.Now().Add(time.Minute).Unix()})
}

func (s *Server) sftpDownload(w http.ResponseWriter, r *http.Request) {
	found := s.listSession(w, r)
	if found == nil {
		return
	}
	client, ok := s.sftpClient(w, r)
	if !ok {
		return
	}
	defer client.Close()
	remotePath := requestedPath(r)
	file, err := client.Open(remotePath)
	if err != nil {
		writeError(w, http.StatusBadGateway, "SFTP_DOWNLOAD_FAILED", err.Error())
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		writeError(w, http.StatusBadRequest, "SFTP_NOT_FILE", "remote path is not a regular file")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(remotePath)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	progress := found.NewTransfer("download", remotePath, info.Size())
	_, copyErr := io.Copy(progress.Writer(w), file)
	progress.Finish(copyErr)
}

func (s *Server) sftpUpload(w http.ResponseWriter, r *http.Request) {
	client, ok := s.sftpClient(w, r)
	if !ok {
		return
	}
	defer client.Close()
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_MULTIPART", err.Error())
		return
	}
	directory := requestedPath(r)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "FILE_REQUIRED", "multipart field file is required")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_MULTIPART", err.Error())
			return
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		name := path.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		if name == "" || name == "." || name == ".." {
			part.Close()
			writeError(w, http.StatusBadRequest, "INVALID_FILENAME", "invalid file name")
			return
		}
		remotePath := path.Join(directory, name)
		file, err := client.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			part.Close()
			writeError(w, http.StatusBadGateway, "SFTP_UPLOAD_FAILED", err.Error())
			return
		}
		written, copyErr := io.CopyBuffer(file, part, make([]byte, 128*1024))
		closeErr := file.Close()
		part.Close()
		if copyErr != nil || closeErr != nil {
			_ = client.Remove(remotePath)
			writeError(w, http.StatusBadGateway, "SFTP_UPLOAD_FAILED", fmt.Sprintf("%v %v", copyErr, closeErr))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "written": written})
		return
	}
}

func (s *Server) newTicket(sessionID, purpose string) string {
	value := randomToken(24)
	s.ticketMu.Lock()
	s.tickets[value] = ticket{sessionID: sessionID, purpose: purpose, expiresAt: time.Now().Add(time.Minute)}
	s.ticketMu.Unlock()
	return value
}

func (s *Server) consumeTicket(value, sessionID, purpose string) bool {
	if value == "" {
		return false
	}
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	found, ok := s.tickets[value]
	delete(s.tickets, value)
	return ok && found.sessionID == sessionID && found.purpose == purpose && found.expiresAt.After(time.Now())
}

func (s *Server) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !s.originAllowed(r, origin) {
			writeError(w, http.StatusForbidden, "ORIGIN_DENIED", "request origin is not allowed")
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "valid bearer token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	if r.URL.Path == "/ws/terminal" {
		return true
	}
	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if ok && subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1 {
		return true
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method == http.MethodGet && len(parts) == 4 && parts[0] == "api" && parts[1] == "sftp" && parts[3] == "download" {
		return s.consumeTicket(r.URL.Query().Get("ticket"), parts[2], "download")
	}
	return false
}

func (s *Server) originAllowed(r *http.Request, origin string) bool {
	if origin == "" {
		return true
	}
	if configured := os.Getenv("TINDOMIEL_ORIGIN"); configured != "" {
		return origin == configured
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	hostname := parsed.Hostname()
	if hostname != "localhost" && hostname != "127.0.0.1" && hostname != "tauri.localhost" {
		return false
	}
	if parsed.Host == r.Host || parsed.Scheme == "tauri" {
		return true
	}
	return parsed.Scheme == "http" && (parsed.Port() == "1420" || parsed.Port() == "5173")
}

func requestedPath(r *http.Request) string {
	if value := r.URL.Query().Get("path"); value != "" {
		return value
	}
	return "."
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorDetails(w, status, code, message, nil)
}

func writeErrorDetails(w http.ResponseWriter, status int, code, message string, details any) {
	body := map[string]any{"code": code, "message": message}
	if details != nil {
		body["details"] = details
	}
	writeJSON(w, status, map[string]any{"error": body})
}

func randomToken(size int) string {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
