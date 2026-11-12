package session

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func newStaticMetricsTestSession(osName string, execCommand func(context.Context, string) ([]byte, error)) *Session {
	return &Session{
		generation: 1,
		metrics: metricsState{
			os:           osName,
			osGeneration: 1,
		},
		execCommand: execCommand,
		pingFunc:    func(context.Context) float64 { return 12.5 },
	}
}

func TestStaticMetricsCacheSkipsStaticCommands(t *testing.T) {
	tests := []struct {
		name         string
		osName       string
		output       func(bool) string
		staticMarker string
	}{
		{name: "linux", osName: "linux", output: linuxTestMetrics, staticMarker: "echo __SYSTEM__"},
		{name: "macOS", osName: "darwin", output: darwinTestMetrics, staticMarker: "echo __SYSTEM__"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var commands []string
			session := newStaticMetricsTestSession(test.osName, func(_ context.Context, command string) ([]byte, error) {
				commands = append(commands, command)
				return []byte(test.output(strings.Contains(command, test.staticMarker))), nil
			})
			first, err := session.Metrics(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			second, err := session.Metrics(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(commands) != 2 {
				t.Fatalf("got %d commands, want 2", len(commands))
			}
			if !strings.Contains(commands[0], test.staticMarker) {
				t.Fatalf("initial command omitted static metrics: %s", commands[0])
			}
			if strings.Contains(commands[1], test.staticMarker) {
				t.Fatalf("cached command still collects static metrics: %s", commands[1])
			}
			if first.System.Hostname != "test-host" || second.System != first.System {
				t.Fatalf("cached system changed: first %#v, second %#v", first.System, second.System)
			}
			if second.PingMs != 12.5 {
				t.Fatalf("unexpected ping value: %v", second.PingMs)
			}
			if first.Timestamp <= 0 || second.Timestamp < first.Timestamp {
				t.Fatalf("unexpected metric timestamps: first %d, second %d", first.Timestamp, second.Timestamp)
			}
		})
	}
}

func TestPowerShellStaticMetricsCacheSkipsStaticQueries(t *testing.T) {
	var scripts []string
	session := newStaticMetricsTestSession("windows", func(_ context.Context, command string) ([]byte, error) {
		script, ok := powerShellScript(command)
		if !ok {
			return nil, fmt.Errorf("unexpected command: %s", command)
		}
		scripts = append(scripts, script)
		return []byte(windowsPowerShellTestMetrics(strings.Contains(script, "$cpuInfo="))), nil
	})
	first, err := session.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 2 || !strings.Contains(scripts[0], "Win32_ComputerSystem") {
		t.Fatalf("unexpected initial scripts: %#v", scripts)
	}
	if strings.Contains(scripts[1], "Win32_ComputerSystem") || strings.Contains(scripts[1], "$cpuInfo=") || strings.Contains(scripts[1], `"SYS|`) {
		t.Fatalf("cached script still collects static metrics: %s", scripts[1])
	}
	if !strings.Contains(scripts[1], "Win32_OperatingSystem") {
		t.Fatalf("cached script lost dynamic memory and uptime query: %s", scripts[1])
	}
	if second.System != first.System || second.Memory.Total != first.Memory.Total {
		t.Fatalf("unexpected cached metrics: first %#v, second %#v", first, second)
	}
}

func TestCmdStaticMetricsCacheRetainsTotals(t *testing.T) {
	var commands []string
	session := newStaticMetricsTestSession("windows", func(_ context.Context, command string) ([]byte, error) {
		commands = append(commands, command)
		return []byte(cmdTestMetrics(strings.Contains(command, "systeminfo"))), nil
	})
	session.metrics.windowsCollector = "cmd"
	first, err := session.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || !strings.Contains(commands[0], "systeminfo") {
		t.Fatalf("unexpected commands: %#v", commands)
	}
	if strings.Contains(commands[1], "systeminfo") || strings.Contains(commands[1], "reg query") || strings.Contains(commands[1], "__ENV__") {
		t.Fatalf("cached command still collects static metrics: %s", commands[1])
	}
	if second.System != first.System || second.Memory.Total != first.Memory.Total || second.Swap.Total != first.Swap.Total {
		t.Fatalf("cached totals changed: first %#v, second %#v", first, second)
	}
}

func TestStaticMetricsCacheRetriesAfterFailure(t *testing.T) {
	var commands []string
	fail := true
	session := newStaticMetricsTestSession("linux", func(_ context.Context, command string) ([]byte, error) {
		commands = append(commands, command)
		if fail {
			fail = false
			return nil, errors.New("static query failed")
		}
		return []byte(linuxTestMetrics(strings.Contains(command, "echo __SYSTEM__"))), nil
	})
	if _, err := session.Metrics(context.Background()); err == nil {
		t.Fatal("expected initial metrics failure")
	}
	if session.metrics.static != nil {
		t.Fatalf("failed collection populated cache: %#v", session.metrics.static)
	}
	if _, err := session.Metrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := session.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 || !strings.Contains(commands[1], "echo __SYSTEM__") || strings.Contains(commands[2], "echo __SYSTEM__") {
		t.Fatalf("failure was cached or retry did not populate cache: %#v", commands)
	}
	if result.System.Hostname != "test-host" {
		t.Fatalf("unexpected system: %#v", result.System)
	}
}

func TestStaticMetricsCacheDoesNotCacheIncompleteSystem(t *testing.T) {
	var commands []string
	session := newStaticMetricsTestSession("linux", func(_ context.Context, command string) ([]byte, error) {
		commands = append(commands, command)
		if len(commands) == 1 {
			return []byte("__UPTIME__\n1 0\n__CPU__\n__MEM__\n__NET__\n__DISK__\n__PROC__\n"), nil
		}
		return []byte(linuxTestMetrics(strings.Contains(command, "echo __SYSTEM__"))), nil
	})
	if _, err := session.Metrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	if session.metrics.static != nil {
		t.Fatalf("incomplete system populated cache: %#v", session.metrics.static)
	}
	if _, err := session.Metrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Metrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(commands[1], "echo __SYSTEM__") || strings.Contains(commands[2], "echo __SYSTEM__") {
		t.Fatalf("incomplete system was not recollected: %#v", commands)
	}
}

func TestStaticMetricsCacheInvalidatedOnReconnect(t *testing.T) {
	var commands []string
	session := newStaticMetricsTestSession("linux", func(_ context.Context, command string) ([]byte, error) {
		commands = append(commands, command)
		return []byte(linuxTestMetricsAtHost(strings.Contains(command, "echo __SYSTEM__"), "host-generation-1")), nil
	})
	first, err := session.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	session.generation++
	session.mu.Unlock()
	session.execCommand = func(_ context.Context, command string) ([]byte, error) {
		commands = append(commands, command)
		if command == "uname -s" {
			return []byte("Linux\n"), nil
		}
		return []byte(linuxTestMetricsAtHost(strings.Contains(command, "echo __SYSTEM__"), "host-generation-2")), nil
	}
	second, err := session.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	third, err := session.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.System.Hostname != "host-generation-1" || second.System.Hostname != "host-generation-2" || third.System != second.System {
		t.Fatalf("reconnect reused stale system: first %#v, second %#v, third %#v", first.System, second.System, third.System)
	}
	if len(commands) != 4 || commands[1] != "uname -s" || !strings.Contains(commands[2], "echo __SYSTEM__") || strings.Contains(commands[3], "echo __SYSTEM__") {
		t.Fatalf("reconnect did not recollect static metrics: %#v", commands)
	}
}

func linuxTestMetrics(withSystem bool) string {
	return linuxTestMetricsAtHost(withSystem, "test-host")
}

func linuxTestMetricsAtHost(withSystem bool, hostname string) string {
	var output strings.Builder
	output.WriteString("__UPTIME__\n120 0\n1.0 1.5 2.0\n")
	if withSystem {
		fmt.Fprintf(&output, "__SYSTEM__\n%s\n6.1.0\nx86_64\n8\nPRETTY_NAME=\"Test Linux\"\n__CPUINFO__\nmodel name : Test CPU\n", hostname)
	}
	output.WriteString("__CPU__\ncpu 100 0 100 800 0 0 0 0 0 0\n")
	output.WriteString("__MEM__\nMemTotal: 1024 kB\nMemAvailable: 512 kB\nSwapTotal: 128 kB\nSwapFree: 64 kB\n")
	output.WriteString("__NET__\nInter-| Receive | Transmit\n")
	output.WriteString("__DISK__\nFilesystem 1-blocks Used Available Use% Mounted on\n")
	output.WriteString("__PROC__\n1 1.5 10 init\n")
	return output.String()
}

func darwinTestMetrics(withSystem bool) string {
	var output strings.Builder
	fmt.Fprintf(&output, "__UPTIME__\n{ sec = %d, usec = 0 }\n{ 1.0 1.5 2.0 }\n", time.Now().Unix()-120)
	if withSystem {
		output.WriteString("__SYSTEM__\ntest-host\n23.0.0\narm64\n8\nmacOS\n15.0\nApple M3\n")
	}
	output.WriteString("__CPU__\nCPU usage: 10.0% user, 5.0% sys, 85.0% idle\n")
	output.WriteString("__MEM__\n17179869184\nMach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 100.\nPages inactive: 50.\nPages speculative: 25.\n")
	output.WriteString("__SWAP__\ntotal = 2048.00M  used = 512.00M  free = 1536.00M\n")
	output.WriteString("__NET__\nName Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll\n")
	output.WriteString("__DISK__\nFilesystem 1024-blocks Used Available Capacity iused ifree %iused Mounted on\n")
	output.WriteString("__PROC__\n1 1.5 10 launchd\n")
	return output.String()
}

func windowsPowerShellTestMetrics(withSystem bool) string {
	var output strings.Builder
	if withSystem {
		output.WriteString("SYS|test-host|Microsoft Windows 11|10.0.22631|AMD64|Test CPU|16\n")
	}
	output.WriteString("CPU|12.5\nUPTIME|120\nMEM|17179869184|8589934592\nSWAP|2147483648|1073741824\n")
	output.WriteString("PROC|1|2.5|1048576|pwsh\nNET|Ethernet|100|200\nDISK|C:|1000000|400000\n")
	return output.String()
}

func cmdTestMetrics(withSystem bool) string {
	var output strings.Builder
	if withSystem {
		output.WriteString("__SYSTEM__\n")
		values := make([]string, 25)
		values[0] = "test-host"
		values[1] = "Microsoft Windows 11"
		values[2] = "10.0.22631 N/A Build 22631"
		values[22] = "65,536 MB"
		values[24] = "81,920 MB"
		writer := csv.NewWriter(&output)
		_ = writer.Write(make([]string, len(values)))
		_ = writer.Write(values)
		writer.Flush()
		output.WriteString("__ENV__\n16\nAMD64\n__CPUINFO__\nProcessorNameString    REG_SZ    Test CPU\n")
	}
	output.WriteString("__COUNTERS__\n")
	writer := csv.NewWriter(&output)
	_ = writer.Write([]string{
		"Timestamp",
		`\\test-host\Processor(_Total)\% Processor Time`,
		`\\test-host\System\System Up Time`,
		`\\test-host\Memory\Available Bytes`,
		`\\test-host\Paging File(_Total)\% Usage`,
		`\\test-host\Process(pwsh)\ID Process`,
		`\\test-host\Process(pwsh)\% Processor Time`,
		`\\test-host\Process(pwsh)\Working Set - Private`,
	})
	_ = writer.Write([]string{"now", "12.5", "120", "8589934592", "50", "1", "2.5", "1048576"})
	writer.Flush()
	return output.String()
}
