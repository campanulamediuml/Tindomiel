package sshconn

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Verification struct {
	VerificationID string `json:"verificationId"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	KeyType        string `json:"keyType"`
	Fingerprint    string `json:"fingerprint"`
	address        string
	publicKey      ssh.PublicKey
	expiresAt      time.Time
}

type HostKeyError struct {
	Kind         string
	Verification *Verification
}

func (e *HostKeyError) Error() string {
	if e.Kind == "HOST_KEY_CHANGED" {
		return "host key changed"
	}
	return "unknown host key"
}

type HostKeys struct {
	path    string
	mu      sync.Mutex
	check   ssh.HostKeyCallback
	pending map[string]*Verification
}

func NewHostKeys(path string) (*HostKeys, error) {
	store := &HostKeys{path: path, pending: make(map[string]*Verification)}
	if err := store.reload(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *HostKeys) Verify(hostname string, remote net.Addr, key ssh.PublicKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.check != nil {
		err := s.check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyError *knownhosts.KeyError
		if !errors.As(err, &keyError) {
			return err
		}
		if len(keyError.Want) > 0 {
			slog.Warn("host_key_changed", "address", hostname, "fingerprint", ssh.FingerprintSHA256(key))
			return &HostKeyError{Kind: "HOST_KEY_CHANGED"}
		}
	}

	verification := &Verification{
		Host:        hostname,
		KeyType:     key.Type(),
		Fingerprint: ssh.FingerprintSHA256(key),
		address:     hostname,
		publicKey:   key,
		expiresAt:   time.Now().Add(5 * time.Minute),
	}
	verification.VerificationID = verificationID()
	verification.Host, verification.Port = splitAddress(hostname)
	s.pending[verification.VerificationID] = verification
	slog.Info("host_key_unknown", "address", hostname, "fingerprint", verification.Fingerprint, "keyType", verification.KeyType)
	return &HostKeyError{Kind: "HOST_KEY_UNKNOWN", Verification: verification}
}

func (s *HostKeys) Accept(id, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	verification, ok := s.pending[id]
	if !ok || verification.expiresAt.Before(time.Now()) {
		delete(s.pending, id)
		return errors.New("host key verification expired")
	}
	if verification.Fingerprint != fingerprint {
		return errors.New("host key fingerprint mismatch")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	line := knownhosts.Line([]string{verification.address}, verification.publicKey)
	_, writeErr := fmt.Fprintln(file, line)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	delete(s.pending, id)
	slog.Info("host_key_accepted", "address", verification.address, "fingerprint", verification.Fingerprint)
	return s.reload()
}

func (s *HostKeys) reload() error {
	check, err := knownhosts.New(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.check = nil
		return nil
	}
	if err != nil {
		return fmt.Errorf("load known_hosts: %w", err)
	}
	s.check = check
	return nil
}

func splitAddress(address string) (string, int) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return address, 22
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		return host, 22
	}
	return host, port
}

func verificationID() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return "hk_" + hex.EncodeToString(data)
}
