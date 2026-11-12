package session

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"
)

const windowsNetworkCommand = `chcp 65001 >nul & typeperf "\Network Interface(*)\Bytes Received/sec" "\Network Interface(*)\Bytes Sent/sec" -si 1`

func (s *Session) windowsNetworkMonitorLoop() {
	for {
		s.mu.RLock()
		generation := s.generation
		closed := s.closed
		s.mu.RUnlock()
		if closed {
			return
		}
		s.publishNetwork(generation, time.Now(), nil, fmt.Errorf("starting Windows network sampler"))
		err := s.streamWindowsNetwork(context.Background(), generation)
		s.mu.RLock()
		closed = s.closed
		s.mu.RUnlock()
		if closed {
			return
		}
		s.publishNetwork(generation, time.Now(), nil, err)
		if !waitForMonitor(s.monitorStop, time.Second) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), networkSampleTimeout)
		s.networkOS(ctx, generation)
		cancel()
	}
}

func (s *Session) streamWindowsNetwork(ctx context.Context, generation uint64) error {
	client, err := s.Client()
	if err != nil {
		return err
	}
	remote, err := client.NewSession()
	if err != nil {
		return err
	}
	defer func() {
		_ = remote.Close()
		_ = remote.Wait()
	}()
	stdout, err := remote.StdoutPipe()
	if err != nil {
		return err
	}
	remote.Stderr = io.Discard
	if err := remote.Start(windowsNetworkCommand); err != nil {
		return err
	}
	type readResult struct {
		interfaces []InterfaceMetrics
		err        error
	}
	results := make(chan readResult, 1)
	readerStop := make(chan struct{})
	defer close(readerStop)
	send := func(result readResult) bool {
		select {
		case results <- result:
			return true
		case <-readerStop:
			return false
		}
	}
	go func() {
		reader := csv.NewReader(stdout)
		reader.FieldsPerRecord = -1
		headers, err := reader.Read()
		if err != nil {
			send(readResult{err: err})
			return
		}
		for {
			values, err := reader.Read()
			if err != nil {
				send(readResult{err: err})
				return
			}
			interfaces, err := parseWindowsNetworkRecord(headers, values)
			if err != nil {
				send(readResult{err: err})
				return
			}
			if !send(readResult{interfaces: interfaces}) {
				return
			}
		}
	}()
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	var sampledAt time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.monitorStop:
			return context.Canceled
		case <-timer.C:
			return fmt.Errorf("Windows network sample timed out")
		case result := <-results:
			if result.err != nil {
				return result.err
			}
			if sampledAt.IsZero() {
				sampledAt = time.Now()
			} else {
				sampledAt = sampledAt.Add(time.Second)
			}
			s.publishNetwork(generation, sampledAt, result.interfaces, nil)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(2500 * time.Millisecond)
		}
	}
}

func parseWindowsNetworkRecord(headers, values []string) ([]InterfaceMetrics, error) {
	if len(headers) < 2 || len(values) < 2 {
		return nil, fmt.Errorf("incomplete Windows network sample")
	}
	counters := make(map[windowsCounter]float64, len(headers)-1)
	for index := 1; index < len(headers) && index < len(values); index++ {
		key, ok := parseWindowsCounter(headers[index])
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(values[index], 64)
		if err != nil {
			return nil, fmt.Errorf("parse Windows network rate: %w", err)
		}
		counters[key] = value
	}
	interfaces := parseCmdInterfaces(counters)
	if len(interfaces) == 0 {
		return nil, fmt.Errorf("Windows network sample contains no interfaces")
	}
	return interfaces, nil
}
