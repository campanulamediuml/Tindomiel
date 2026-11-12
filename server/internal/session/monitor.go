package session

import (
	"context"
	"hash/fnv"
	"log/slog"
	"time"
)

func (s *Session) startMonitor() {
	s.monitorWG.Add(2)
	go s.monitorLoop()
	go s.networkMonitorLoop()
}

func monitorInitialDelay(id string) time.Duration {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(id))
	return time.Duration(hash.Sum64() % uint64(time.Second))
}

func waitForMonitor(stop <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-stop:
		return false
	case <-timer.C:
		return true
	}
}

func (s *Session) monitorLoop() {
	defer s.monitorWG.Done()
	if !waitForMonitor(s.monitorStop, monitorInitialDelay(s.id)) {
		return
	}
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
		if !waitForMonitor(s.monitorStop, delay) {
			return
		}
	}
}
