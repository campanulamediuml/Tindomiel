package session

import (
	"context"
	"log/slog"
	"time"
)

func (s *Session) startMonitor() {
	s.monitorWG.Add(1)
	go s.monitorLoop()
}

func (s *Session) monitorLoop() {
	defer s.monitorWG.Done()
	for {
		s.mu.RLock()
		closed := s.closed
		s.mu.RUnlock()
		if closed {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		snapshot, err := s.Metrics(ctx)
		cancel()
		delay := time.Second
		if err != nil {
			delay = 3 * time.Second
			s.mu.RLock()
			closed = s.closed
			s.mu.RUnlock()
			if !closed {
				slog.Warn("metrics_failed", "sessionId", s.id, "error", err)
			}
		} else {
			s.sendControl(map[string]any{"type": "metrics", "metrics": snapshot})
		}
		timer := time.NewTimer(delay)
		select {
		case <-s.monitorStop:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
