package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/user"
	"sync"
	"time"

	"tindomiel/internal/configstore"
	"tindomiel/internal/sshconn"

	"github.com/coder/websocket"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	maxPendingOutput = 256 * 1024
	maxReconnectTry  = 5
	LocalProfileID   = "local:shell"
)

type Status string

const (
	Connecting   Status = "connecting"
	Ready        Status = "ready"
	Disconnected Status = "disconnected"
	Reconnecting Status = "reconnecting"
	Closed       Status = "closed"
	Error        Status = "error"
)

type Info struct {
	SessionID string `json:"sessionId"`
	ProfileID string `json:"profileId"`
	Status    Status `json:"status"`
	OpenedAt  int64  `json:"openedAt"`
}

type Manager struct {
	store    *configstore.Store
	hostKeys *sshconn.HostKeys
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewManager(store *configstore.Store, hostKeys *sshconn.HostKeys) *Manager {
	return &Manager{store: store, hostKeys: hostKeys, sessions: make(map[string]*Session)}
}

func (m *Manager) Create(ctx context.Context, profileID string, cols, rows int) (*Session, error) {
	profile, err := localProfile(profileID)
	if profileID != LocalProfileID {
		profile, err = m.store.Get(ctx, profileID)
	}
	if err != nil {
		return nil, err
	}
	session := &Session{
		id:          sessionID(),
		profile:     profile,
		manager:     m,
		status:      Connecting,
		openedAt:    time.Now(),
		cols:        cols,
		rows:        rows,
		monitorStop: make(chan struct{}),
	}
	session.editors = newEditorManager(session)
	slog.Info("session_creating", "sessionId", session.id, "profileId", profile.ID, "profile", profile.Name)
	if err := session.open(Connecting); err != nil {
		slog.Error("session_create_failed", "sessionId", session.id, "profileId", profile.ID, "error", err)
		return nil, err
	}
	session.startMonitor()
	m.mu.Lock()
	m.sessions[session.id] = session
	m.mu.Unlock()
	slog.Info("session_created", "sessionId", session.id)
	return session, nil
}

func localProfile(profileID string) (configstore.Profile, error) {
	if profileID != LocalProfileID {
		return configstore.Profile{}, nil
	}
	current, err := user.Current()
	if err != nil {
		return configstore.Profile{}, fmt.Errorf("read current user: %w", err)
	}
	keys := sshconn.DefaultPrivateKeys()
	paths := make([]string, 0, len(keys))
	for _, key := range keys {
		paths = append(paths, key.Path)
	}
	return configstore.Profile{
		ID:     LocalProfileID,
		Name:   "本机 Shell",
		Source: "local",
		Options: configstore.Options{
			Host:        "127.0.0.1",
			Port:        22,
			User:        current.Username,
			Auth:        "publicKey",
			PrivateKeys: paths,
		},
	}, nil
}

func (m *Manager) Get(id string) (*Session, error) {
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session == nil {
		return nil, errors.New("session not found")
	}
	return session, nil
}

func (m *Manager) List() []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	infos := make([]Info, 0, len(m.sessions))
	for _, session := range m.sessions {
		infos = append(infos, session.Info())
	}
	return infos
}

func (m *Manager) Delete(id string) error {
	slog.Info("session_close_requested", "sessionId", id)
	m.mu.Lock()
	session := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if session == nil {
		return errors.New("session not found")
	}
	session.Close()
	return nil
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
}

type Session struct {
	id      string
	profile configstore.Profile
	manager *Manager

	mu              sync.RWMutex
	client          *ssh.Client
	shell           *ssh.Session
	stdin           io.WriteCloser
	ws              *websocket.Conn
	status          Status
	openedAt        time.Time
	cols            int
	rows            int
	generation      uint64
	closed          bool
	reconnectActive bool
	pending         []byte
	metrics         metricsState
	editors         *editorManager
	cwd             string

	writeMu        sync.Mutex
	sftpMu         sync.Mutex
	sftp           *sftp.Client
	sftpGeneration uint64
	newSFTPClient  func(*ssh.Client) (*sftp.Client, error)
	fileTimesMu    sync.RWMutex
	fileTimesOS    string
	monitorStop    chan struct{}
	monitorWG      sync.WaitGroup
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) Info() Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Info{SessionID: s.id, ProfileID: s.profile.ID, Status: s.status, OpenedAt: s.openedAt.Unix()}
}

func (s *Session) Cwd() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cwd
}

func (s *Session) Client() (*ssh.Client, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.status != Ready || s.client == nil {
		return nil, errors.New("SSH session is not ready")
	}
	return s.client, nil
}

func (s *Session) SFTPClient() (*sftp.Client, error) {
	s.sftpMu.Lock()
	defer s.sftpMu.Unlock()
	s.mu.RLock()
	if s.status != Ready || s.client == nil {
		s.mu.RUnlock()
		return nil, errors.New("SSH session is not ready")
	}
	sshClient := s.client
	generation := s.generation
	cached := s.sftp
	cachedGeneration := s.sftpGeneration
	s.mu.RUnlock()
	if cached != nil && cachedGeneration == generation {
		return cached, nil
	}
	factory := s.newSFTPClient
	if factory == nil {
		factory = func(client *ssh.Client) (*sftp.Client, error) {
			return sftp.NewClient(client)
		}
	}
	created, err := factory(sshClient)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.status != Ready || s.client != sshClient || s.generation != generation {
		s.mu.Unlock()
		_ = created.Close()
		return nil, errors.New("SSH session is not ready")
	}
	previous := s.sftp
	s.sftp = created
	s.sftpGeneration = generation
	s.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return created, nil
}

func (s *Session) closeSFTP() {
	s.sftpMu.Lock()
	s.mu.Lock()
	client := s.sftp
	s.sftp = nil
	s.sftpGeneration = 0
	s.mu.Unlock()
	s.sftpMu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

func (s *Session) WriteInput(data []byte) error {
	s.mu.RLock()
	stdin := s.stdin
	ready := s.status == Ready && !s.closed
	s.mu.RUnlock()
	if !ready || stdin == nil {
		return errors.New("terminal input is disabled while disconnected")
	}
	_, err := stdin.Write(data)
	return err
}

func (s *Session) Resize(cols, rows int) error {
	if cols < 2 || rows < 2 || cols > 1000 || rows > 500 {
		return errors.New("invalid terminal size")
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	shell := s.shell
	ready := s.status == Ready
	s.mu.Unlock()
	if ready && shell != nil {
		return shell.WindowChange(rows, cols)
	}
	return nil
}

func (s *Session) Attach(conn *websocket.Conn) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("session is closed")
	}
	s.ws = conn
	pending := append([]byte(nil), s.pending...)
	s.pending = nil
	status := s.status
	s.mu.Unlock()
	if len(pending) > 0 {
		if err := s.writeFrame(conn, websocket.MessageBinary, pending); err != nil {
			return err
		}
	}
	return s.sendStatusTo(conn, status, "")
}

func (s *Session) Detach(conn *websocket.Conn) {
	s.mu.Lock()
	if s.ws == conn {
		s.ws = nil
	}
	s.mu.Unlock()
}

func (s *Session) Reconnect() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("session is closed")
	}
	if s.status == Ready || s.status == Connecting {
		s.mu.Unlock()
		return errors.New("session is already connected")
	}
	if s.reconnectActive {
		s.mu.Unlock()
		return nil
	}
	s.reconnectActive = true
	s.status = Reconnecting
	s.mu.Unlock()
	s.sendStatus(Reconnecting, "")
	go s.reconnectLoop()
	return nil
}

func (s *Session) Close() {
	s.editors.close()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.status = Closed
	s.generation++
	client := s.client
	stdin := s.stdin
	conn := s.ws
	s.client = nil
	s.shell = nil
	s.stdin = nil
	s.ws = nil
	s.mu.Unlock()
	s.closeSFTP()
	close(s.monitorStop)
	if stdin != nil {
		stdin.Close()
	}
	if client != nil {
		client.Close()
	}
	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "session closed")
	}
	s.monitorWG.Wait()
}

func (s *Session) open(initialStatus Status) error {
	s.mu.RLock()
	cols, rows := s.cols, s.rows
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return errors.New("session is closed")
	}
	s.closeSFTP()
	client, err := sshconn.Dial(s.profile, s.manager.hostKeys)
	if err != nil {
		return err
	}
	shell, err := client.NewSession()
	if err != nil {
		client.Close()
		return err
	}
	stdin, err := shell.StdinPipe()
	if err != nil {
		client.Close()
		return err
	}
	stdout, err := shell.StdoutPipe()
	if err != nil {
		client.Close()
		return err
	}
	stderr, err := shell.StderrPipe()
	if err != nil {
		client.Close()
		return err
	}
	windowsShell := bytes.Contains(client.Conn.ServerVersion(), []byte("OpenSSH_for_Windows"))
	echo := uint32(0)
	if windowsShell {
		echo = 1
	}
	modes := ssh.TerminalModes{ssh.ECHO: echo, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := shell.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		client.Close()
		return err
	}
	if err := shell.Shell(); err != nil {
		client.Close()
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		client.Close()
		return errors.New("session is closed")
	}
	s.generation++
	generation := s.generation
	s.client = client
	s.shell = shell
	s.stdin = stdin
	s.status = Ready
	conn := s.ws
	s.mu.Unlock()

	slog.Info("session_ready", "sessionId", s.id, "generation", generation)
	if !windowsShell {
		if integration := shellIntegration(); integration != "" {
			_, _ = stdin.Write([]byte(integration))
		}
	}
	go s.forward(conn, stdout)
	go s.forward(conn, stderr)
	go s.watch(client, shell, generation)
	go s.keepalive(client, generation)
	s.sendStatus(Ready, "")
	return nil
}

func (s *Session) forward(_ *websocket.Conn, reader io.Reader) {
	parser := &osc7Parser{}
	buffer := make([]byte, 32*1024)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			clean, cwd, found := parser.feed(buffer[:count])
			if len(clean) > 0 {
				s.emit(clean)
			}
			if found {
				s.mu.Lock()
				s.cwd = cwd
				s.mu.Unlock()
				s.sendControl(map[string]any{"type": "cwd", "path": cwd})
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) watch(client *ssh.Client, shell *ssh.Session, generation uint64) {
	err := shell.Wait()
	client.Close()
	s.mu.Lock()
	if generation != s.generation || s.closed {
		s.mu.Unlock()
		return
	}
	var exitError *ssh.ExitError
	cleanExit := err == nil || errors.As(err, &exitError)
	if cleanExit {
		s.status = Closed
		s.closed = true
	} else {
		s.status = Disconnected
	}
	s.client = nil
	s.shell = nil
	s.stdin = nil
	s.mu.Unlock()
	s.closeSFTP()
	if cleanExit {
		slog.Info("session_closed", "sessionId", s.id, "code", exitCode(err))
		s.sendControl(map[string]any{"type": "exit", "code": exitCode(err)})
		return
	}
	slog.Warn("session_disconnected", "sessionId", s.id, "error", err)
	s.sendStatus(Disconnected, fmt.Sprintf("SSH connection lost: %v", err))
	s.Reconnect()
}

func (s *Session) keepalive(client *ssh.Client, generation uint64) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.RLock()
		current := generation == s.generation && !s.closed
		s.mu.RUnlock()
		if !current {
			return
		}
		if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
			client.Close()
			return
		}
	}
}

func (s *Session) reconnectLoop() {
	defer func() {
		s.mu.Lock()
		s.reconnectActive = false
		s.mu.Unlock()
	}()
	for attempt := 1; attempt <= maxReconnectTry; attempt++ {
		s.mu.RLock()
		closed := s.closed
		s.mu.RUnlock()
		if closed {
			return
		}
		slog.Info("session_reconnecting", "sessionId", s.id, "attempt", attempt)
		s.sendStatus(Reconnecting, "")
		if attempt > 1 {
			time.Sleep(time.Duration(attempt-1) * 2 * time.Second)
		}
		if err := s.open(Reconnecting); err == nil {
			slog.Info("session_reconnected", "sessionId", s.id, "attempt", attempt)
			return
		} else {
			slog.Warn("session_reconnect_failed", "sessionId", s.id, "attempt", attempt, "error", err)
		}
	}
	slog.Warn("session_reconnect_exhausted", "sessionId", s.id)
	s.sendStatus(Disconnected, "automatic reconnect failed; reconnect manually")
}

func (s *Session) emit(data []byte) {
	s.mu.Lock()
	conn := s.ws
	if conn == nil {
		s.pending = append(s.pending, data...)
		if len(s.pending) > maxPendingOutput {
			s.pending = s.pending[len(s.pending)-maxPendingOutput:]
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if err := s.writeFrame(conn, websocket.MessageBinary, data); err != nil {
		s.Detach(conn)
	}
}

func (s *Session) sendStatus(status Status, message string) {
	s.mu.RLock()
	conn := s.ws
	s.mu.RUnlock()
	if conn != nil {
		s.sendStatusTo(conn, status, message)
	}
}

func (s *Session) sendStatusTo(conn *websocket.Conn, status Status, message string) error {
	payload := map[string]any{"type": "status", "status": status}
	if message != "" {
		payload["message"] = message
	}
	return s.sendJSON(conn, payload)
}

func (s *Session) sendControl(payload map[string]any) {
	s.mu.RLock()
	conn := s.ws
	s.mu.RUnlock()
	if conn != nil {
		s.sendJSON(conn, payload)
	}
}

func (s *Session) SendError(message string) error {
	s.mu.RLock()
	conn := s.ws
	s.mu.RUnlock()
	if conn == nil {
		return errors.New("websocket is not attached")
	}
	return s.sendJSON(conn, map[string]any{"type": "error", "message": message})
}

func (s *Session) sendJSON(conn *websocket.Conn, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.writeFrame(conn, websocket.MessageText, data)
}

func (s *Session) writeFrame(conn *websocket.Conn, messageType websocket.MessageType, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return conn.Write(context.Background(), messageType, data)
}

func exitCode(err error) int {
	var exitError *ssh.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitStatus()
	}
	return 0
}

func sessionID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return "s_" + hex.EncodeToString(data)
}
