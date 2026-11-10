package session

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *Session) darwinMetrics(ctx context.Context) (ResourceMetrics, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	output, err := s.exec(ctx, `echo __UPTIME__; sysctl -n kern.boottime; sysctl -n vm.loadavg; echo __SYSTEM__; hostname; uname -r; uname -m; sysctl -n hw.ncpu; sw_vers -productName; sw_vers -productVersion; sysctl -n machdep.cpu.brand_string; echo __CPU__; top -l 2 -n 0 -s 1 -stats cpu | grep 'CPU usage'; echo __MEM__; sysctl -n hw.memsize; vm_stat; echo __SWAP__; sysctl -n vm.swapusage; echo __NET__; netstat -ibn; echo __DISK__; df -k; echo __PROC__; ps -axo pid=,pcpu=,rss=,args=`)
	if err != nil {
		return ResourceMetrics{}, fmt.Errorf("read macOS metrics: %w", err)
	}
	sections := splitSections(string(output))
	result := ResourceMetrics{
		System:      parseDarwinSystem(sections["SYSTEM"]),
		LoadAverage: parseDarwinLoad(sections["UPTIME"]),
		CPUPercent:  parseDarwinCPU(sections["CPU"]),
		Processes:   parseProcesses(sections["PROC"]),
		Disks:       parseDarwinDF(sections["DISK"]),
	}
	result.UptimeSeconds = parseDarwinUptime(sections["UPTIME"])
	result.Memory = parseDarwinMemory(sections["MEM"])
	result.Swap = parseDarwinSwap(sections["SWAP"])
	result.Interfaces = s.networkRates(parseDarwinNetwork(sections["NET"]))
	return result, nil
}

func parseDarwinSystem(output string) SystemMetrics {
	result := SystemMetrics{OSName: "macOS"}
	lines := make([]string, 0, 7)
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
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
	if len(lines) > 5 {
		result.OSName = lines[4] + " " + lines[5]
	}
	if len(lines) > 6 {
		result.CPUModel = lines[6]
	}
	return result
}

func parseDarwinUptime(output string) float64 {
	fields := strings.Fields(output)
	for index := 0; index+2 < len(fields); index++ {
		if fields[index] == "sec" && fields[index+1] == "=" {
			seconds, _ := strconv.ParseInt(strings.TrimSuffix(fields[index+2], ","), 10, 64)
			if seconds > 0 {
				return float64(time.Now().Unix() - seconds)
			}
			break
		}
	}
	return 0
}

func parseDarwinLoad(output string) []float64 {
	start := strings.LastIndex(output, "{")
	if start < 0 {
		return []float64{}
	}
	values := strings.Trim(output[start:], "{} \r\n")
	result := make([]float64, 0, 3)
	for _, field := range strings.Fields(values) {
		if len(result) == 3 {
			break
		}
		value, err := strconv.ParseFloat(field, 64)
		if err == nil {
			result = append(result, value)
		}
	}
	return result
}

func parseDarwinCPU(output string) float64 {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return 0
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 7 {
		return 0
	}
	idle, err := strconv.ParseFloat(strings.TrimSuffix(fields[6], "%"), 64)
	if err != nil {
		return 0
	}
	return 100 - idle
}

func parseDarwinMemory(output string) MemoryMetrics {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return MemoryMetrics{}
	}
	total, _ := strconv.ParseUint(strings.TrimSpace(lines[0]), 10, 64)
	var pageSize uint64
	pages := make(map[string]uint64)
	for _, line := range lines[1:] {
		if start := strings.Index(line, "page size of "); start >= 0 {
			value := line[start+len("page size of "):]
			if end := strings.Index(value, " bytes"); end >= 0 {
				pageSize, _ = strconv.ParseUint(value[:end], 10, 64)
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		pages[key], _ = strconv.ParseUint(strings.TrimSuffix(fields[0], "."), 10, 64)
	}
	available := (pages["Pages free"] + pages["Pages inactive"] + pages["Pages speculative"]) * pageSize
	used := subtract(total, available)
	return MemoryMetrics{Total: total, Used: used, Available: available, UsedPercent: percentage(used, total)}
}

func parseDarwinSwap(output string) SwapMetrics {
	var total, used uint64
	fields := strings.Fields(strings.ReplaceAll(output, "=", " "))
	for index := 0; index+1 < len(fields); index++ {
		value := parseDarwinSize(fields[index+1])
		switch fields[index] {
		case "total":
			total = value
		case "used":
			used = value
		}
	}
	return SwapMetrics{Total: total, Used: used, UsedPercent: percentage(used, total)}
}

func parseDarwinSize(value string) uint64 {
	if len(value) < 2 {
		return 0
	}
	unit := value[len(value)-1]
	number, err := strconv.ParseFloat(value[:len(value)-1], 64)
	if err != nil {
		return 0
	}
	multiplier := float64(1)
	switch unit {
	case 'K':
		multiplier = 1 << 10
	case 'M':
		multiplier = 1 << 20
	case 'G':
		multiplier = 1 << 30
	case 'T':
		multiplier = 1 << 40
	}
	return uint64(number * multiplier)
}

func parseDarwinNetwork(output string) map[string]networkSample {
	result := make(map[string]networkSample)
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || !strings.HasPrefix(fields[2], "<Link#") || fields[0] == "lo0" {
			continue
		}
		if _, exists := result[fields[0]]; exists {
			continue
		}
		rx, _ := strconv.ParseUint(fields[6], 10, 64)
		tx, _ := strconv.ParseUint(fields[9], 10, 64)
		result[fields[0]] = networkSample{rx: rx, tx: tx}
	}
	return result
}

func parseDarwinDF(output string) []DiskMetrics {
	var result []DiskMetrics
	for index, line := range strings.Split(output, "\n") {
		if index == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		total, _ := strconv.ParseUint(fields[1], 10, 64)
		used, _ := strconv.ParseUint(fields[2], 10, 64)
		available, _ := strconv.ParseUint(fields[3], 10, 64)
		if total == 0 {
			continue
		}
		result = append(result, DiskMetrics{Path: strings.Join(fields[8:], " "), Total: total * 1024, Used: used * 1024, Available: available * 1024, UsedPercent: percentage(used, total)})
	}
	return result
}
