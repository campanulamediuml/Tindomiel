package session

import (
	"context"
	"encoding/json"
	"fmt"
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
}

func (s *Session) Metrics(ctx context.Context) (ResourceMetrics, error) {
	s.metrics.mu.Lock()
	defer s.metrics.mu.Unlock()
	if s.metrics.os == "" {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		output, err := s.exec(ctx, "uname -s")
		cancel()
		if err == nil && strings.Contains(strings.ToLower(string(output)), "linux") {
			s.metrics.os = "linux"
		} else {
			s.metrics.os = "windows"
		}
	}
	var (
		result ResourceMetrics
		err    error
	)
	if s.metrics.os == "linux" {
		result, err = s.linuxMetrics(ctx)
	} else {
		result, err = s.windowsMetrics(ctx)
	}
	if err != nil {
		return ResourceMetrics{}, err
	}
	result.PingMs = s.ping(ctx)
	return result, nil
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
		`$system=[pscustomobject]@{hostname=$env:COMPUTERNAME;osName=$os.Caption;kernelVersion=$os.Version;architecture=$env:PROCESSOR_ARCHITECTURE;cpuModel=$cpuInfo.Name;cpuCores=$computer.NumberOfLogicalProcessors};` +
		`$p=Get-CimInstance Win32_PerfFormattedData_PerfProc_Process|Where-Object {$_.Name -ne '_Total' -and $_.Name -ne 'Idle'}|Select-Object IDProcess,PercentProcessorTime,WorkingSetPrivate,Name;` +
		`$n=Get-CimInstance Win32_PerfFormattedData_Tcpip_NetworkInterface|Select-Object Name,BytesReceivedPersec,BytesSentPersec;` +
		`$d=Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3'|Select-Object DeviceID,Size,FreeSpace;` +
		`$swapTotal=($os.TotalVirtualMemorySize-$os.TotalVisibleMemorySize)*1024;` +
		`$swapUsed=$swapTotal-($os.FreeVirtualMemory-$os.FreePhysicalMemory)*1024;` +
		`[pscustomobject]@{system=$system;cpu=$cpu;uptime=((Get-Date)-$os.LastBootUpTime).TotalSeconds;memoryTotal=$os.TotalVisibleMemorySize*1024;memoryAvailable=$os.FreePhysicalMemory*1024;swapTotal=$swapTotal;swapUsed=$swapUsed;processes=$p;interfaces=$n;disks=$d}|ConvertTo-Json -Compress -Depth 4`
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := s.exec(ctx, `powershell -NoProfile -NonInteractive -Command "`+script+`"`)
	if err != nil {
		return ResourceMetrics{}, fmt.Errorf("read Windows metrics: %w", err)
	}
	var raw struct {
		System          SystemMetrics `json:"system"`
		CPU             float64       `json:"cpu"`
		Uptime          float64       `json:"uptime"`
		MemoryTotal     uint64        `json:"memoryTotal"`
		MemoryAvailable uint64        `json:"memoryAvailable"`
		SwapTotal       uint64        `json:"swapTotal"`
		SwapUsed        uint64        `json:"swapUsed"`
		Processes       []struct {
			PID    int     `json:"IDProcess"`
			CPU    float64 `json:"PercentProcessorTime"`
			Memory uint64  `json:"WorkingSetPrivate"`
			Name   string  `json:"Name"`
		} `json:"processes"`
		Interfaces []struct {
			Name string  `json:"Name"`
			Rx   float64 `json:"BytesReceivedPersec"`
			Tx   float64 `json:"BytesSentPersec"`
		} `json:"interfaces"`
		Disks []struct {
			Path string `json:"DeviceID"`
			Size uint64 `json:"Size"`
			Free uint64 `json:"FreeSpace"`
		} `json:"disks"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return ResourceMetrics{}, fmt.Errorf("parse Windows metrics: %w", err)
	}
	result := ResourceMetrics{System: raw.System, UptimeSeconds: raw.Uptime, LoadAverage: []float64{}, CPUPercent: raw.CPU}
	used := subtract(raw.MemoryTotal, raw.MemoryAvailable)
	result.Memory = MemoryMetrics{Total: raw.MemoryTotal, Used: used, Available: raw.MemoryAvailable, UsedPercent: percentage(used, raw.MemoryTotal)}
	result.Swap = SwapMetrics{Total: raw.SwapTotal, Used: raw.SwapUsed, UsedPercent: percentage(raw.SwapUsed, raw.SwapTotal)}
	for _, process := range raw.Processes {
		result.Processes = append(result.Processes, ProcessMetrics{PID: process.PID, CPUPercent: process.CPU, MemoryBytes: process.Memory, Command: process.Name})
	}
	for _, iface := range raw.Interfaces {
		result.Interfaces = append(result.Interfaces, InterfaceMetrics{Name: iface.Name, RxBytesPerSecond: iface.Rx, TxBytesPerSecond: iface.Tx})
	}
	for _, disk := range raw.Disks {
		used := subtract(disk.Size, disk.Free)
		result.Disks = append(result.Disks, DiskMetrics{Path: disk.Path, Total: disk.Size, Used: used, Available: disk.Free, UsedPercent: percentage(used, disk.Size)})
	}
	return result, nil
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
		done <- response{output: output, err: err}
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
