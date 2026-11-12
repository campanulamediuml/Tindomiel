package session

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

const networkSampleTimeout = 900 * time.Millisecond

type NetworkMetrics struct {
	Timestamp  int64              `json:"timestamp"`
	Interfaces []InterfaceMetrics `json:"interfaces"`
}

type networkMonitorState struct {
	mu           sync.Mutex
	os           string
	osGeneration uint64
	previous     map[string]networkSample
	sampledAt    time.Time
	latest       NetworkMetrics
}

func (s *Session) networkMonitorLoop() {
	defer s.monitorWG.Done()
	if !waitForMonitor(s.monitorStop, monitorInitialDelay(s.id)) {
		return
	}
	s.mu.RLock()
	generation := s.generation
	s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), networkSampleTimeout)
	osName := s.networkOS(ctx, generation)
	cancel()
	if osName == "windows" {
		s.windowsNetworkMonitorLoop()
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.mu.RLock()
		closed := s.closed
		s.mu.RUnlock()
		if closed {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), networkSampleTimeout)
		s.collectNetwork(ctx, time.Now())
		cancel()
		select {
		case <-s.monitorStop:
			return
		case <-ticker.C:
		}
	}
}

func (s *Session) collectNetwork(ctx context.Context, sampledAt time.Time) {
	s.mu.RLock()
	generation := s.generation
	s.mu.RUnlock()
	osName := s.networkOS(ctx, generation)
	if osName == "" {
		s.publishNetwork(generation, sampledAt, nil, fmt.Errorf("network OS detection failed"))
		return
	}
	interfaces, err := s.readNetwork(ctx, osName)
	if err != nil {
		slog.Debug("network_metrics_failed", "sessionId", s.id, "error", err)
	}
	s.publishNetwork(generation, sampledAt, interfaces, err)
}

func (s *Session) networkOS(ctx context.Context, generation uint64) string {
	s.network.mu.Lock()
	if s.network.osGeneration != generation {
		s.network.os = ""
		s.network.osGeneration = generation
		s.network.previous = nil
		s.network.sampledAt = time.Time{}
		s.network.latest = NetworkMetrics{Interfaces: []InterfaceMetrics{}}
	}
	osName := s.network.os
	s.network.mu.Unlock()
	if osName != "" {
		return osName
	}
	output, err := s.exec(ctx, "uname -s")
	system := strings.ToLower(string(output))
	switch {
	case err == nil && strings.Contains(system, "linux"):
		osName = "linux"
	case err == nil && strings.Contains(system, "darwin"):
		osName = "darwin"
	default:
		osName = "windows"
	}
	s.network.mu.Lock()
	if s.network.osGeneration == generation {
		s.network.os = osName
	}
	s.network.mu.Unlock()
	return osName
}

func (s *Session) readNetwork(ctx context.Context, osName string) ([]InterfaceMetrics, error) {
	switch osName {
	case "linux":
		output, err := s.exec(ctx, "cat /proc/net/dev")
		if err != nil {
			return nil, err
		}
		return cumulativeNetworkRates(parseLinuxNetwork(string(output))), nil
	case "darwin":
		output, err := s.exec(ctx, "netstat -ibn")
		if err != nil {
			return nil, err
		}
		return cumulativeNetworkRates(parseDarwinNetwork(string(output))), nil
	default:
		return nil, fmt.Errorf("Windows network requires the streaming sampler")
	}
}

func cumulativeNetworkRates(samples map[string]networkSample) []InterfaceMetrics {
	result := make([]InterfaceMetrics, 0, len(samples))
	for name, sample := range samples {
		result = append(result, InterfaceMetrics{Name: name, RxBytesPerSecond: float64(sample.rx), TxBytesPerSecond: float64(sample.tx)})
	}
	return result
}

func (s *Session) publishNetwork(generation uint64, sampledAt time.Time, samples []InterfaceMetrics, sampleErr error) {
	s.network.mu.Lock()
	if s.network.osGeneration != generation {
		s.network.mu.Unlock()
		return
	}
	var rates []InterfaceMetrics
	if sampleErr != nil {
		rates = zeroNetworkRates(s.network.latest.Interfaces)
	} else if s.network.os == "windows" {
		rates = samples
	} else {
		current := make(map[string]networkSample, len(samples))
		for _, sample := range samples {
			current[sample.Name] = networkSample{rx: uint64(sample.RxBytesPerSecond), tx: uint64(sample.TxBytesPerSecond)}
		}
		rates = calculateNetworkRates(current, s.network.previous, s.network.sampledAt, sampledAt)
		s.network.previous = current
		s.network.sampledAt = sampledAt
	}
	sort.Slice(rates, func(i, j int) bool { return rates[i].Name < rates[j].Name })
	s.network.latest = NetworkMetrics{Timestamp: sampledAt.Unix(), Interfaces: rates}
	latest := s.network.latest
	s.network.mu.Unlock()
	s.sendControl(map[string]any{"type": "network", "network": latest})
}

func calculateNetworkRates(current, previous map[string]networkSample, previousAt, sampledAt time.Time) []InterfaceMetrics {
	elapsed := sampledAt.Unix() - previousAt.Unix()
	if elapsed < 1 {
		elapsed = 1
	}
	result := make([]InterfaceMetrics, 0, len(current))
	for name, sample := range current {
		var rxRate, txRate float64
		before, ok := previous[name]
		if ok && !previousAt.IsZero() && sample.rx >= before.rx && sample.tx >= before.tx {
			rxRate = float64(sample.rx-before.rx) / float64(elapsed)
			txRate = float64(sample.tx-before.tx) / float64(elapsed)
		}
		result = append(result, InterfaceMetrics{Name: name, RxBytesPerSecond: rxRate, TxBytesPerSecond: txRate})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func zeroNetworkRates(interfaces []InterfaceMetrics) []InterfaceMetrics {
	result := make([]InterfaceMetrics, len(interfaces))
	for index, iface := range interfaces {
		result[index] = InterfaceMetrics{Name: iface.Name}
	}
	return result
}

func (s *Session) currentNetworkInterfaces() []InterfaceMetrics {
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	result := make([]InterfaceMetrics, len(s.network.latest.Interfaces))
	copy(result, s.network.latest.Interfaces)
	return result
}
