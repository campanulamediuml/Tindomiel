package session

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MemoryMetrics struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Available   uint64  `json:"available"`
	UsedPercent float64 `json:"usedPercent"`
}

type SwapMetrics struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	UsedPercent float64 `json:"usedPercent"`
}

type ProcessMetrics struct {
	PID         int     `json:"pid"`
	CPUPercent  float64 `json:"cpuPercent"`
	MemoryBytes uint64  `json:"memoryBytes"`
	Command     string  `json:"command"`
}

type InterfaceMetrics struct {
	Name             string  `json:"name"`
	RxBytesPerSecond float64 `json:"rxBytesPerSecond"`
	TxBytesPerSecond float64 `json:"txBytesPerSecond"`
}

type DiskMetrics struct {
	Path        string  `json:"path"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Available   uint64  `json:"available"`
	UsedPercent float64 `json:"usedPercent"`
}

type SystemMetrics struct {
	Hostname      string `json:"hostname"`
	OSName        string `json:"osName"`
	KernelVersion string `json:"kernelVersion"`
	Architecture  string `json:"architecture"`
	CPUModel      string `json:"cpuModel"`
	CPUCores      int    `json:"cpuCores"`
}

type ResourceMetrics struct {
	System        SystemMetrics      `json:"system"`
	UptimeSeconds float64            `json:"uptimeSeconds"`
	LoadAverage   []float64          `json:"loadAverage"`
	CPUPercent    float64            `json:"cpuPercent"`
	Memory        MemoryMetrics      `json:"memory"`
	Swap          SwapMetrics        `json:"swap"`
	Processes     []ProcessMetrics   `json:"processes"`
	Interfaces    []InterfaceMetrics `json:"interfaces"`
	PingMs        float64            `json:"pingMs"`
	Disks         []DiskMetrics      `json:"disks"`
}

type networkSample struct {
	rx uint64
	tx uint64
}

type metricsState struct {
	mu        sync.Mutex
	os        string
	cpuTotal  uint64
	cpuIdle   uint64
	network   map[string]networkSample
	sampledAt time.Time
	latest    *ResourceMetrics
}

func (s *Session) Metrics(ctx context.Context) (ResourceMetrics, error) {
	s.metrics.mu.Lock()
	defer s.metrics.mu.Unlock()
	if s.metrics.os == "" {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		output, err := s.exec(ctx, "uname -s")
		cancel()
		system := strings.ToLower(string(output))
		switch {
		case err == nil && strings.Contains(system, "linux"):
			s.metrics.os = "linux"
		case err == nil && strings.Contains(system, "darwin"):
			s.metrics.os = "darwin"
		default:
			s.metrics.os = "windows"
		}
	}
	var (
		result ResourceMetrics
		err    error
	)
	switch s.metrics.os {
	case "linux":
		result, err = s.linuxMetrics(ctx)
	case "darwin":
		result, err = s.darwinMetrics(ctx)
	default:
		result, err = s.windowsMetrics(ctx)
	}
	if err != nil {
		return ResourceMetrics{}, err
	}
	result.PingMs = s.ping(ctx)
	s.metrics.latest = &result
	return result, nil
}

func (s *Session) CachedMetrics() (ResourceMetrics, bool) {
	s.metrics.mu.Lock()
	defer s.metrics.mu.Unlock()
	if s.metrics.latest == nil {
		return ResourceMetrics{}, false
	}
	return *s.metrics.latest, true
}

func (s *Session) linuxMetrics(ctx context.Context) (ResourceMetrics, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	output, err := s.exec(ctx, `echo __UPTIME__; cat /proc/uptime; cat /proc/loadavg; echo __SYSTEM__; hostname; uname -r; uname -m; nproc; cat /etc/os-release; echo __CPUINFO__; head -n 30 /proc/cpuinfo; echo __CPU__; head -n 1 /proc/stat; echo __MEM__; cat /proc/meminfo; echo __NET__; cat /proc/net/dev; echo __DISK__; df -P -B1 -x tmpfs -x devtmpfs; echo __PROC__; ps -eo pid=,pcpu=,rss=,args=`)
	if err != nil {
		return ResourceMetrics{}, fmt.Errorf("read Linux metrics: %w", err)
	}
	sections := splitSections(string(output))
	result := ResourceMetrics{System: parseLinuxSystem(sections["SYSTEM"], sections["CPUINFO"]), LoadAverage: []float64{}}
	uptimeLines := strings.Split(strings.TrimSpace(sections["UPTIME"]), "\n")
	if fields := strings.Fields(uptimeLines[0]); len(fields) > 0 {
		result.UptimeSeconds, _ = strconv.ParseFloat(fields[0], 64)
	}
	if len(uptimeLines) > 1 {
		result.LoadAverage = make([]float64, 0, 3)
		for _, value := range strings.Fields(uptimeLines[1]) {
			if len(result.LoadAverage) == 3 {
				break
			}
			load, _ := strconv.ParseFloat(value, 64)
			result.LoadAverage = append(result.LoadAverage, load)
		}
	}
	if fields := strings.Fields(sections["CPU"]); len(fields) >= 8 && fields[0] == "cpu" {
		var values []uint64
		for _, field := range fields[1:] {
			value, _ := strconv.ParseUint(field, 10, 64)
			values = append(values, value)
		}
		var total uint64
		for _, value := range values {
			total += value
		}
		idle := values[3]
		if len(values) > 4 {
			idle += values[4]
		}
		if s.metrics.cpuTotal > 0 && total > s.metrics.cpuTotal && total > s.metrics.cpuTotal+100 {
			result.CPUPercent = 100 * (1 - float64(idle-s.metrics.cpuIdle)/float64(total-s.metrics.cpuTotal))
		}
		s.metrics.cpuTotal, s.metrics.cpuIdle = total, idle
	}

	memory := parseMeminfo(sections["MEM"])
	total, available := memory["MemTotal"], memory["MemAvailable"]
	used := subtract(total, available)
	result.Memory = MemoryMetrics{Total: total, Used: used, Available: available, UsedPercent: percentage(used, total)}
	swapUsed := subtract(memory["SwapTotal"], memory["SwapFree"])
	result.Swap = SwapMetrics{Total: memory["SwapTotal"], Used: swapUsed, UsedPercent: percentage(swapUsed, memory["SwapTotal"])}
	result.Interfaces = s.networkRates(parseLinuxNetwork(sections["NET"]))
	result.Disks = parseDF(sections["DISK"])
	result.Processes = parseProcesses(sections["PROC"])
	return result, nil
}

func (s *Session) windowsMetrics(ctx context.Context) (ResourceMetrics, error) {
	script := `[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding($false);` +
		`$os=Get-CimInstance Win32_OperatingSystem;` +
		`$cpu=(Get-CimInstance Win32_Processor|Measure-Object LoadPercentage -Average).Average;` +
		`$cpuInfo=Get-CimInstance Win32_Processor|Select-Object -First 1 Name,NumberOfLogicalProcessors;` +
		`$computer=Get-CimInstance Win32_ComputerSystem;` +
		`$p=Get-CimInstance Win32_PerfFormattedData_PerfProc_Process|Where-Object {$_.Name -ne '_Total' -and $_.Name -ne 'Idle'}|Select-Object IDProcess,PercentProcessorTime,WorkingSetPrivate,Name;` +
		`$n=Get-CimInstance Win32_PerfFormattedData_Tcpip_NetworkInterface|Select-Object Name,BytesReceivedPersec,BytesSentPersec;` +
		`$d=Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3'|Select-Object DeviceID,Size,FreeSpace;` +
		`$pageFiles=Get-CimInstance Win32_PageFileUsage;` +
		`$swapTotal=($pageFiles|Measure-Object AllocatedBaseSize -Sum).Sum*1MB;` +
		`$swapUsed=($pageFiles|Measure-Object CurrentUsage -Sum).Sum*1MB;` +
		`$uptime=((Get-Date)-$os.LastBootUpTime).TotalSeconds;` +
		`$memoryTotal=$os.TotalVisibleMemorySize*1024;` +
		`$memoryAvailable=$os.FreePhysicalMemory*1024;` +
		`"SYS|$env:COMPUTERNAME|$($os.Caption)|$($os.Version)|$env:PROCESSOR_ARCHITECTURE|$($cpuInfo.Name)|$($computer.NumberOfLogicalProcessors)";` +
		`"CPU|$cpu";"UPTIME|$uptime";"MEM|$memoryTotal|$memoryAvailable";"SWAP|$swapTotal|$swapUsed";` +
		`$p | ForEach-Object { "PROC|$($_.IDProcess)|$($_.PercentProcessorTime)|$($_.WorkingSetPrivate)|$($_.Name)" };` +
		`$n | ForEach-Object { "NET|$($_.Name)|$($_.BytesReceivedPersec)|$($_.BytesSentPersec)" };` +
		`$d | ForEach-Object { "DISK|$($_.DeviceID)|$($_.Size)|$($_.FreeSpace)" }`
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := s.exec(ctx, powershellCommand(script))
	if err != nil {
		return ResourceMetrics{}, fmt.Errorf("read Windows metrics: %w", err)
	}
	return parseWindowsTextMetrics(output), nil
}

func parseWindowsTextMetrics(output []byte) ResourceMetrics {
	result := ResourceMetrics{LoadAverage: []float64{}, Processes: []ProcessMetrics{}, Interfaces: []InterfaceMetrics{}, Disks: []DiskMetrics{}}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "> "))
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}
		switch parts[0] {
		case "SYS":
			if len(parts) >= 7 {
				cores, _ := strconv.Atoi(parts[6])
				result.System = SystemMetrics{Hostname: parts[1], OSName: parts[2], KernelVersion: parts[3], Architecture: parts[4], CPUModel: parts[5], CPUCores: cores}
			}
		case "CPU":
			result.CPUPercent, _ = strconv.ParseFloat(parts[1], 64)
		case "UPTIME":
			result.UptimeSeconds, _ = strconv.ParseFloat(parts[1], 64)
		case "MEM":
			if len(parts) >= 3 {
				total := parseUintText(parts[1])
				available := parseUintText(parts[2])
				used := subtract(total, available)
				result.Memory = MemoryMetrics{Total: total, Used: used, Available: available, UsedPercent: percentage(used, total)}
			}
		case "SWAP":
			if len(parts) >= 3 {
				total := parseUintText(parts[1])
				used := parseUintText(parts[2])
				result.Swap = SwapMetrics{Total: total, Used: used, UsedPercent: percentage(used, total)}
			}
		case "PROC":
			if len(parts) >= 5 {
				pid, _ := strconv.Atoi(parts[1])
				cpu, _ := strconv.ParseFloat(parts[2], 64)
				memory := parseUintText(parts[3])
				result.Processes = append(result.Processes, ProcessMetrics{PID: pid, CPUPercent: cpu, MemoryBytes: memory, Command: strings.Join(parts[4:], "|")})
			}
		case "NET":
			if len(parts) >= 4 {
				rx, _ := strconv.ParseFloat(parts[2], 64)
				tx, _ := strconv.ParseFloat(parts[3], 64)
				result.Interfaces = append(result.Interfaces, InterfaceMetrics{Name: parts[1], RxBytesPerSecond: rx, TxBytesPerSecond: tx})
			}
		case "DISK":
			if len(parts) >= 4 {
				total := parseUintText(parts[2])
				free := parseUintText(parts[3])
				used := subtract(total, free)
				result.Disks = append(result.Disks, DiskMetrics{Path: parts[1], Total: total, Used: used, Available: free, UsedPercent: percentage(used, total)})
			}
		}
	}
	return result
}

func parseUintText(value string) uint64 {
	parsed, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return parsed
}

func (s *Session) exec(ctx context.Context, command string) ([]byte, error) {
	client, err := s.Client()
	if err != nil {
		return nil, err
	}
	remote, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer remote.Close()
	type response struct {
		output []byte
		err    error
	}
	done := make(chan response, 1)
	go func() {
		output, err := remote.CombinedOutput(command)
		if err == nil {
			done <- response{output: output}
			return
		}
		if script, ok := powerShellScript(command); ok {
			raw, rawErr := runPowerShellEncodedResult(ctx, client, script)
			if rawErr == nil {
				done <- response{output: raw}
				return
			}
			slog.Debug("powershell_encoded_result_failed", "error", rawErr)
		}
		fallback, fallbackErr := runShellCommand(ctx, client, command)
		if fallbackErr != nil {
			done <- response{output: fallback, err: fmt.Errorf("exec: %v; shell fallback: %w", err, fallbackErr)}
			return
		}
		done <- response{output: fallback}
	}()
	select {
	case <-ctx.Done():
		_ = remote.Close()
		return nil, ctx.Err()
	case result := <-done:
		return result.output, result.err
	}
}

func (s *Session) ping(ctx context.Context) float64 {
	s.mu.RLock()
	host := s.profile.Options.Host
	s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ping", "-n", "-c", "1", "-W", "1000", host).CombinedOutput()
	if err != nil {
		return 0
	}
	_, value, ok := strings.Cut(string(output), "time=")
	if !ok {
		return 0
	}
	value, _, _ = strings.Cut(value, " ")
	value = strings.TrimSpace(strings.TrimSuffix(value, "ms"))
	milliseconds, _ := strconv.ParseFloat(value, 64)
	return milliseconds
}

func (s *Session) networkRates(current map[string]networkSample) []InterfaceMetrics {
	now := time.Now()
	elapsed := now.Sub(s.metrics.sampledAt).Seconds()
	result := make([]InterfaceMetrics, 0, len(current))
	for name, sample := range current {
		var rxRate, txRate float64
		previous, ok := s.metrics.network[name]
		if ok && elapsed > 0 && sample.rx >= previous.rx && sample.tx >= previous.tx {
			rxRate = float64(sample.rx-previous.rx) / elapsed
			txRate = float64(sample.tx-previous.tx) / elapsed
		}
		result = append(result, InterfaceMetrics{Name: name, RxBytesPerSecond: rxRate, TxBytesPerSecond: txRate})
	}
	s.metrics.network = current
	s.metrics.sampledAt = now
	return result
}

func splitSections(output string) map[string]string {
	sections := make(map[string]string)
	var current string
	var builder strings.Builder
	flush := func() {
		if current != "" {
			sections[current] = strings.TrimSpace(builder.String())
		}
		builder.Reset()
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if name, ok := strings.CutPrefix(line, "__"); ok {
			if name, valid := strings.CutSuffix(name, "__"); valid {
				flush()
				current = name
				continue
			}
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	flush()
	return sections
}

func parseLinuxSystem(output, cpuInfo string) SystemMetrics {
	result := SystemMetrics{OSName: "Linux"}
	lines := make([]string, 0, 8)
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	if len(lines) > 0 {
		result.Hostname = lines[0]
	}
	if len(lines) > 1 {
		result.KernelVersion = lines[1]
	}
	if len(lines) > 2 {
		result.Architecture = lines[2]
	}
	if len(lines) > 3 {
		result.CPUCores, _ = strconv.Atoi(lines[3])
	}
	if len(lines) > 4 {
		for _, line := range lines[4:] {
			if name, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				result.OSName = strings.Trim(name, `"`)
			}
		}
	}
	for line := range strings.SplitSeq(cpuInfo, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "model name") {
			result.CPUModel = strings.TrimSpace(value)
			break
		}
	}
	return result
}

func parseMeminfo(output string) map[string]uint64 {
	result := make(map[string]uint64)
	for line := range strings.SplitSeq(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		number, _ := strconv.ParseUint(fields[0], 10, 64)
		result[key] = number * 1024
	}
	return result
}

func parseLinuxNetwork(output string) map[string]networkSample {
	result := make(map[string]networkSample)
	for line := range strings.SplitSeq(output, "\n") {
		name, values, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(values)
		if len(fields) < 9 || strings.TrimSpace(name) == "lo" {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		result[strings.TrimSpace(name)] = networkSample{rx: rx, tx: tx}
	}
	return result
}

func parseDF(output string) []DiskMetrics {
	var result []DiskMetrics
	for index, line := range strings.Split(output, "\n") {
		if index == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		total, _ := strconv.ParseUint(fields[1], 10, 64)
		used, _ := strconv.ParseUint(fields[2], 10, 64)
		available, _ := strconv.ParseUint(fields[3], 10, 64)
		if total == 0 {
			continue
		}
		result = append(result, DiskMetrics{Path: strings.Join(fields[5:], " "), Total: total, Used: used, Available: available, UsedPercent: percentage(used, total)})
	}
	return result
}

func parseProcesses(output string) []ProcessMetrics {
	var result []ProcessMetrics
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		cpu, _ := strconv.ParseFloat(fields[1], 64)
		rss, _ := strconv.ParseUint(fields[2], 10, 64)
		result = append(result, ProcessMetrics{PID: pid, CPUPercent: cpu, MemoryBytes: rss * 1024, Command: strings.Join(fields[3:], " ")})
	}
	return result
}

func percentage(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(part) / float64(total)
}

func subtract(total, available uint64) uint64 {
	if available > total {
		return 0
	}
	return total - available
}
