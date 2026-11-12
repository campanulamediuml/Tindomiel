package session

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestMonitorInitialDelayIsDeterministic(t *testing.T) {
	first := monitorInitialDelay("session-a")
	second := monitorInitialDelay("session-b")
	if first != monitorInitialDelay("session-a") || second != monitorInitialDelay("session-b") {
		t.Fatal("same session ID produced different initial delays")
	}
	if first < 0 || first >= time.Second || second < 0 || second >= time.Second {
		t.Fatalf("delay outside initial sampling interval: %v, %v", first, second)
	}
	if first == second {
		t.Fatalf("test session IDs were not staggered: %v", first)
	}
}

func TestCloseInterruptsInitialMonitorDelay(t *testing.T) {
	id := ""
	for index := 0; index < 100; index++ {
		candidate := fmt.Sprintf("close-session-%d", index)
		if monitorInitialDelay(candidate) >= 500*time.Millisecond {
			id = candidate
			break
		}
	}
	if id == "" {
		t.Fatal("could not find test session with a long initial delay")
	}
	var calls atomic.Int32
	session := &Session{
		id:          id,
		monitorStop: make(chan struct{}),
		execCommand: func(context.Context, string) ([]byte, error) {
			calls.Add(1)
			return nil, fmt.Errorf("unexpected metrics call")
		},
	}
	session.startMonitor()
	started := time.Now()
	session.Close()
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("Close waited for initial monitor delay: %v", elapsed)
	}
	if calls.Load() != 0 {
		t.Fatalf("metrics ran after Close: %d calls", calls.Load())
	}
}
