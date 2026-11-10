package session

import (
	"io"
	"path"
	"strings"
	"sync"
	"time"
)

type TransferState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Direction   string `json:"direction"`
	Path        string `json:"path"`
	CurrentPath string `json:"currentPath,omitempty"`
	Loaded      int64  `json:"loaded"`
	Total       int64  `json:"total"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

type TransferProgress struct {
	session  *Session
	state    TransferState
	mu       sync.Mutex
	lastSent time.Time
	finished bool
}

func (s *Session) NewTransfer(direction, remotePath string, total int64) *TransferProgress {
	progress := &TransferProgress{session: s, state: TransferState{
		ID:        strings.Replace(sessionID(), "s_", "t_", 1),
		Name:      path.Base(remotePath),
		Direction: direction,
		Path:      remotePath,
		Total:     total,
		Status:    "running",
	}}
	progress.emit(false)
	return progress
}

func (p *TransferProgress) Writer(target io.Writer) io.Writer {
	return &transferWriter{target: target, progress: p}
}

func (p *TransferProgress) SetCurrentPath(remotePath string) {
	p.mu.Lock()
	p.state.CurrentPath = remotePath
	p.lastSent = time.Time{}
	p.emitLocked()
	p.mu.Unlock()
}

func (p *TransferProgress) Finish(err error) {
	p.mu.Lock()
	if p.finished {
		p.mu.Unlock()
		return
	}
	p.finished = true
	if err != nil {
		p.state.Status = "failed"
		p.state.Error = err.Error()
	} else {
		p.state.Status = "complete"
		if p.state.Total > 0 {
			p.state.Loaded = p.state.Total
		}
	}
	p.emitLocked()
	p.mu.Unlock()
}

func (p *TransferProgress) add(count int) {
	p.mu.Lock()
	p.state.Loaded += int64(count)
	p.emitLocked()
	p.mu.Unlock()
}

func (p *TransferProgress) emit(force bool) {
	p.mu.Lock()
	p.emitLocked()
	p.mu.Unlock()
}

func (p *TransferProgress) emitLocked() {
	now := time.Now()
	if !p.finished && now.Sub(p.lastSent) < 100*time.Millisecond {
		return
	}
	p.lastSent = now
	p.session.sendControl(map[string]any{"type": "transfer", "transfer": p.state})
}

type transferWriter struct {
	target   io.Writer
	progress *TransferProgress
}

func (w *transferWriter) Write(data []byte) (int, error) {
	count, err := w.target.Write(data)
	w.progress.add(count)
	return count, err
}
