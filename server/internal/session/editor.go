package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pkg/sftp"
)

type editorManager struct {
	session *Session
	mu      sync.Mutex
	files   map[string]*watchedFile
}

type watchedFile struct {
	remotePath string
	cachePath  string
	watcher    *fsnotify.Watcher
	done       chan struct{}
	closeOnce  sync.Once
}

func newEditorManager(session *Session) *editorManager {
	return &editorManager{session: session, files: make(map[string]*watchedFile)}
}

func (s *Session) OpenForEdit(remotePath, application string) (string, error) {
	if s.editors == nil {
		return "", errors.New("editor service is unavailable")
	}
	return s.editors.open(remotePath, application)
}

func (m *editorManager) open(remotePath, application string) (string, error) {
	if !path.IsAbs(remotePath) || path.Base(remotePath) == "." {
		return "", errors.New("an absolute remote file path is required")
	}
	m.mu.Lock()
	if existing := m.files[remotePath]; existing != nil {
		m.mu.Unlock()
		return existing.cachePath, openLocal(existing.cachePath, application)
	}
	m.mu.Unlock()

	client, err := m.session.Client()
	if err != nil {
		return "", err
	}
	remote, err := sftp.NewClient(client)
	if err != nil {
		return "", err
	}
	defer remote.Close()
	source, err := remote.Open(remotePath)
	if err != nil {
		return "", err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || info.IsDir() {
		return "", errors.New("remote path is not a regular file")
	}
	cachePath, err := editorCachePath(m.session.id, remotePath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		return "", err
	}
	progress := m.session.NewTransfer("download", remotePath, info.Size())
	if err := downloadToCache(source, cachePath, progress); err != nil {
		return "", err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return "", err
	}
	if err := watcher.Add(filepath.Dir(cachePath)); err != nil {
		watcher.Close()
		return "", err
	}
	file := &watchedFile{remotePath: remotePath, cachePath: cachePath, watcher: watcher, done: make(chan struct{})}
	m.mu.Lock()
	m.files[remotePath] = file
	m.mu.Unlock()
	go m.watch(file)
	if err := openLocal(cachePath, application); err != nil {
		return cachePath, err
	}
	return cachePath, nil
}

func (m *editorManager) watch(file *watchedFile) {
	var timer *time.Timer
	var timerChannel <-chan time.Time
	resetTimer := func() {
		if timer == nil {
			timer = time.NewTimer(450 * time.Millisecond)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(450 * time.Millisecond)
		}
		timerChannel = timer.C
	}
	defer file.watcher.Close()
	for {
		select {
		case <-file.done:
			return
		case event, ok := <-file.watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) == file.cachePath && event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
				resetTimer()
			}
		case <-timerChannel:
			timerChannel = nil
			m.upload(file)
		case err, ok := <-file.watcher.Errors:
			if ok {
				m.session.sendControl(map[string]any{"type": "sftp", "action": "save-error", "path": file.remotePath, "message": fmt.Sprintf("监听本地保存失败：%v", err)})
			}
		}
	}
}

func (m *editorManager) upload(file *watchedFile) {
	local, err := os.Open(file.cachePath)
	if err != nil {
		m.notifySaveError(file.remotePath, err)
		return
	}
	defer local.Close()
	client, err := m.session.Client()
	if err != nil {
		m.notifySaveError(file.remotePath, err)
		return
	}
	remote, err := sftp.NewClient(client)
	if err != nil {
		m.notifySaveError(file.remotePath, err)
		return
	}
	defer remote.Close()
	target, err := remote.OpenFile(file.remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		m.notifySaveError(file.remotePath, err)
		return
	}
	_, copyErr := io.CopyBuffer(target, local, make([]byte, 128*1024))
	closeErr := target.Close()
	if copyErr != nil || closeErr != nil {
		m.notifySaveError(file.remotePath, errors.Join(copyErr, closeErr))
		return
	}
	m.session.sendControl(map[string]any{"type": "sftp", "action": "saved", "path": file.remotePath, "message": "已自动回传 " + file.remotePath})
}

func (m *editorManager) notifySaveError(remotePath string, err error) {
	m.session.sendControl(map[string]any{"type": "sftp", "action": "save-error", "path": remotePath, "message": fmt.Sprintf("回传 %s 失败：%v", remotePath, err)})
}

func (m *editorManager) close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	files := m.files
	m.files = make(map[string]*watchedFile)
	m.mu.Unlock()
	for _, file := range files {
		file.closeOnce.Do(func() { close(file.done) })
		file.watcher.Close()
	}
}

func editorCachePath(sessionID, remotePath string) (string, error) {
	cacheRoot := os.Getenv("TINDOMIEL_CACHE")
	if cacheRoot == "" {
		var err error
		cacheRoot, err = os.UserCacheDir()
		if err != nil {
			return "", err
		}
	}
	hash := sha256.Sum256([]byte(remotePath))
	directory := hex.EncodeToString(hash[:8])
	name := path.Base(strings.ReplaceAll(remotePath, "\\", "/"))
	return filepath.Join(cacheRoot, "Tindomiel", sessionID, directory, name), nil
}

func downloadToCache(source *sftp.File, cachePath string, progress *TransferProgress) (err error) {
	defer func() { progress.Finish(err) }()
	temporary, err := os.CreateTemp(filepath.Dir(cachePath), ".download-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := io.CopyBuffer(progress.Writer(temporary), source, make([]byte, 128*1024)); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, cachePath)
}

func openLocal(cachePath, application string) error {
	arguments := []string{}
	if application != "" {
		arguments = append(arguments, "-a", application)
	}
	arguments = append(arguments, cachePath)
	command := exec.Command("open", arguments...)
	if err := command.Start(); err != nil {
		return fmt.Errorf("open local editor: %w", err)
	}
	return command.Process.Release()
}
