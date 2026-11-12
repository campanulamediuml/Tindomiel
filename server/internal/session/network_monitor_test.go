package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNetworkFailureProducesZeroAndDelayedSuccessUsesElapsedSeconds(t *testing.T) {
	fail := false
	successfulReads := 0
	session := &Session{
		generation: 1,
		network: networkMonitorState{
			os:           "linux",
			osGeneration: 1,
		},
		execCommand: func(_ context.Context, command string) ([]byte, error) {
			if command != "cat /proc/net/dev" {
				t.Fatalf("unexpected command: %s", command)
			}
			if fail {
				return nil, errors.New("sample timeout")
			}
			successfulReads++
			if successfulReads == 1 {
				return []byte("eth0: 100 0 0 0 0 0 0 0 200\n"), nil
			}
			return []byte("eth0: 400 0 0 0 0 0 0 0 1000\n"), nil
		},
	}

	session.collectNetwork(context.Background(), time.Unix(1000, 0))
	assertNetworkRates(t, session.currentNetworkInterfaces(), 0, 0)
	fail = true
	session.collectNetwork(context.Background(), time.Unix(1001, 0))
	assertNetworkRates(t, session.currentNetworkInterfaces(), 0, 0)
	if session.network.sampledAt.Unix() != 1000 {
		t.Fatalf("failed sample advanced the cumulative baseline: %v", session.network.sampledAt)
	}
	fail = false
	session.collectNetwork(context.Background(), time.Unix(1004, 0))
	assertNetworkRates(t, session.currentNetworkInterfaces(), 75, 200)
	if session.network.latest.Timestamp != 1004 {
		t.Fatalf("unexpected network timestamp: %d", session.network.latest.Timestamp)
	}
}

func TestWindowsNetworkUsesDirectRates(t *testing.T) {
	session := &Session{
		generation: 1,
		execCommand: func(_ context.Context, command string) ([]byte, error) {
			return nil, errors.New("uname is unavailable on Windows")
		},
	}
	if osName := session.networkOS(context.Background(), 1); osName != "windows" {
		t.Fatalf("got OS %q, want Windows", osName)
	}
	rates := []InterfaceMetrics{{Name: "Ethernet", RxBytesPerSecond: 123, TxBytesPerSecond: 456}}
	session.publishNetwork(1, time.Unix(1000, 0), rates, nil)
	assertNetworkRates(t, session.currentNetworkInterfaces(), 123, 456)
	session.publishNetwork(1, time.Unix(1001, 0), rates, nil)
	assertNetworkRates(t, session.currentNetworkInterfaces(), 123, 456)
}

func TestParseWindowsNetworkRecord(t *testing.T) {
	headers := []string{"Timestamp", `\\SOCAT\Network Interface(Ethernet)\Bytes Received/sec`, `\\SOCAT\Network Interface(Ethernet)\Bytes Sent/sec`}
	values := []string{"now", "123.5", "456.5"}
	rates, err := parseWindowsNetworkRecord(headers, values)
	if err != nil {
		t.Fatal(err)
	}
	assertNetworkRates(t, rates, 123.5, 456.5)
}

func TestNetworkCounterResetProducesZeroAndNewBaseline(t *testing.T) {
	previousAt := time.Unix(1000, 0)
	previous := map[string]networkSample{"eth0": {rx: 500, tx: 600}}
	current := map[string]networkSample{"eth0": {rx: 100, tx: 200}}
	rates := calculateNetworkRates(current, previous, previousAt, time.Unix(1003, 0))
	assertNetworkRates(t, rates, 0, 0)

	rates = calculateNetworkRates(map[string]networkSample{"eth0": {rx: 400, tx: 500}}, current, time.Unix(1003, 0), time.Unix(1004, 0))
	assertNetworkRates(t, rates, 300, 300)
}

func assertNetworkRates(t *testing.T, rates []InterfaceMetrics, rx, tx float64) {
	t.Helper()
	if len(rates) != 1 || rates[0].Name != "eth0" && rates[0].Name != "Ethernet" {
		t.Fatalf("unexpected interfaces: %#v", rates)
	}
	if rates[0].RxBytesPerSecond != rx || rates[0].TxBytesPerSecond != tx {
		t.Fatalf("got RX %.2f TX %.2f, want RX %.2f TX %.2f", rates[0].RxBytesPerSecond, rates[0].TxBytesPerSecond, rx, tx)
	}
}
