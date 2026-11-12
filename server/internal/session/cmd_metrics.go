package session

import (
	"context"
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type windowsCounter struct {
	object   string
	instance string
	counter  string
}

func (s *Session) cmdMetrics(ctx context.Context, generation uint64) (ResourceMetrics, error) {
	static, cached := s.cachedStaticMetrics(generation)
	command := `chcp 65001 >nul & echo __SYSTEM__ & systeminfo /FO CSV & echo __ENV__ & echo %NUMBER_OF_PROCESSORS% & echo %PROCESSOR_ARCHITECTURE% & echo __CPUINFO__ & reg query "HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0" /v ProcessorNameString & echo __COUNTERS__ & typeperf "\Processor(_Total)\% Processor Time" "\System\System Up Time" "\Memory\Available Bytes" "\Paging File(*)\% Usage" "\Process(*)\ID Process" "\Process(*)\% Processor Time" "\Process(*)\Working Set - Private" "\LogicalDisk(*)\Free Megabytes" "\LogicalDisk(*)\% Free Space" -sc 1`
	if cached {
		command = `chcp 65001 >nul & echo __COUNTERS__ & typeperf "\Processor(_Total)\% Processor Time" "\System\System Up Time" "\Memory\Available Bytes" "\Paging File(*)\% Usage" "\Process(*)\ID Process" "\Process(*)\% Processor Time" "\Process(*)\Working Set - Private" "\LogicalDisk(*)\Free Megabytes" "\LogicalDisk(*)\% Free Space" -sc 1`
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := s.exec(ctx, command)
	if err != nil {
		return ResourceMetrics{}, fmt.Errorf("read cmd metrics: %w", err)
	}
	sections := splitSections(string(output))
	result := ResourceMetrics{LoadAverage: []float64{}, Processes: []ProcessMetrics{}, Interfaces: []InterfaceMetrics{}, Disks: []DiskMetrics{}}
	if cached {
		result.System = static.system
		result.Memory.Total = static.memoryTotal
		result.Swap.Total = static.swapTotal
	} else {
		result, err = parseCmdSystem(sections["SYSTEM"], sections["ENV"], sections["CPUINFO"])
		if err != nil {
			return ResourceMetrics{}, err
		}
	}
	counters, err := parseWindowsCounters(sections["COUNTERS"])
	if err != nil {
		return ResourceMetrics{}, err
	}
	result.CPUPercent = counters[windowsCounter{object: "Processor", instance: "_Total", counter: "% Processor Time"}]
	result.UptimeSeconds = counters[windowsCounter{object: "System", instance: "", counter: "System Up Time"}]
	available := uint64(counters[windowsCounter{object: "Memory", instance: "", counter: "Available Bytes"}])
	used := subtract(result.Memory.Total, available)
	result.Memory.Used = used
	result.Memory.Available = available
	result.Memory.UsedPercent = percentage(used, result.Memory.Total)
	swapUsage := counters[windowsCounter{object: "Paging File", instance: "_Total", counter: "% Usage"}]
	result.Swap.Used = uint64(float64(result.Swap.Total) * swapUsage / 100)
	result.Swap.UsedPercent = percentage(result.Swap.Used, result.Swap.Total)
	result.Processes = parseCmdProcesses(counters)
	result.Interfaces = parseCmdInterfaces(counters)
	result.Disks = parseCmdDisks(counters)
	if result.System.OSName == "" || result.Memory.Total == 0 || len(result.Processes) == 0 {
		return ResourceMetrics{}, fmt.Errorf("cmd returned incomplete metrics")
	}
	return result, nil
}

func parseCmdSystem(output, environment, cpuInfo string) (ResourceMetrics, error) {
	reader := csv.NewReader(strings.NewReader(strings.TrimSpace(output)))
	reader.FieldsPerRecord = -1
	if _, err := reader.Read(); err != nil {
		return ResourceMetrics{}, fmt.Errorf("parse systeminfo header: %w", err)
	}
	values, err := reader.Read()
	if err != nil {
		return ResourceMetrics{}, fmt.Errorf("parse systeminfo values: %w", err)
	}
	if len(values) < 25 {
		return ResourceMetrics{}, fmt.Errorf("systeminfo returned %d fields", len(values))
	}
	result := ResourceMetrics{LoadAverage: []float64{}, Processes: []ProcessMetrics{}, Interfaces: []InterfaceMetrics{}, Disks: []DiskMetrics{}}
	result.System = SystemMetrics{Hostname: values[0], OSName: values[1], KernelVersion: strings.Fields(values[2])[0]}
	environmentLines := strings.Fields(strings.TrimSpace(environment))
	if len(environmentLines) > 0 {
		result.System.CPUCores, _ = strconv.Atoi(environmentLines[0])
	}
	if len(environmentLines) > 1 {
		result.System.Architecture = environmentLines[1]
	}
	for line := range strings.SplitSeq(cpuInfo, "\n") {
		if _, value, ok := strings.Cut(line, "REG_SZ"); ok {
			result.System.CPUModel = strings.TrimSpace(value)
		}
	}
	result.Memory.Total = parseWindowsMegabytes(values[22])
	virtualTotal := parseWindowsMegabytes(values[24])
	result.Swap.Total = subtract(virtualTotal, result.Memory.Total)
	return result, nil
}

func parseWindowsMegabytes(value string) uint64 {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "MB"))
	value = strings.ReplaceAll(value, ",", "")
	number, _ := strconv.ParseFloat(value, 64)
	return uint64(number * (1 << 20))
}

func parseWindowsCounters(output string) (map[windowsCounter]float64, error) {
	reader := csv.NewReader(strings.NewReader(strings.TrimSpace(output)))
	reader.FieldsPerRecord = -1
	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("parse typeperf header: %w", err)
	}
	values, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("parse typeperf values: %w", err)
	}
	result := make(map[windowsCounter]float64, len(headers)-1)
	for index := 1; index < len(headers) && index < len(values); index++ {
		key, ok := parseWindowsCounter(headers[index])
		if !ok {
			continue
		}
		result[key], _ = strconv.ParseFloat(values[index], 64)
	}
	return result, nil
}

func parseWindowsCounter(header string) (windowsCounter, bool) {
	for _, object := range []string{"System", "Memory"} {
		if _, counter, ok := strings.Cut(header, `\`+object+`\`); ok {
			return windowsCounter{object: object, counter: counter}, true
		}
	}
	for _, object := range []string{"Processor", "Paging File", "Process", "Network Interface", "LogicalDisk"} {
		_, remainder, ok := strings.Cut(header, `\`+object+`(`)
		if !ok {
			continue
		}
		end := strings.LastIndex(remainder, `)\`)
		if end < 0 {
			continue
		}
		return windowsCounter{object: object, instance: remainder[:end], counter: remainder[end+2:]}, true
	}
	return windowsCounter{}, false
}

func parseCmdProcesses(counters map[windowsCounter]float64) []ProcessMetrics {
	type processValues struct {
		pid    int
		cpu    float64
		memory uint64
	}
	processes := make(map[string]processValues)
	for counter, value := range counters {
		if counter.object != "Process" || counter.instance == "Idle" || counter.instance == "_Total" {
			continue
		}
		current := processes[counter.instance]
		switch counter.counter {
		case "ID Process":
			current.pid = int(value)
		case "% Processor Time":
			current.cpu = value
		case "Working Set - Private":
			current.memory = uint64(value)
		}
		processes[counter.instance] = current
	}
	result := make([]ProcessMetrics, 0, len(processes))
	for name, process := range processes {
		if process.pid > 0 {
			result = append(result, ProcessMetrics{PID: process.pid, CPUPercent: process.cpu, MemoryBytes: process.memory, Command: name})
		}
	}
	return result
}

func parseCmdInterfaces(counters map[windowsCounter]float64) []InterfaceMetrics {
	interfaces := make(map[string]InterfaceMetrics)
	for counter, value := range counters {
		if counter.object != "Network Interface" {
			continue
		}
		current := interfaces[counter.instance]
		current.Name = counter.instance
		switch counter.counter {
		case "Bytes Received/sec":
			current.RxBytesPerSecond = value
		case "Bytes Sent/sec":
			current.TxBytesPerSecond = value
		}
		interfaces[counter.instance] = current
	}
	result := make([]InterfaceMetrics, 0, len(interfaces))
	for _, network := range interfaces {
		result = append(result, network)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func parseCmdDisks(counters map[windowsCounter]float64) []DiskMetrics {
	type diskValues struct {
		free        uint64
		freePercent float64
	}
	disks := make(map[string]diskValues)
	for counter, value := range counters {
		if counter.object != "LogicalDisk" || len(counter.instance) != 2 || counter.instance[1] != ':' {
			continue
		}
		current := disks[counter.instance]
		switch counter.counter {
		case "Free Megabytes":
			current.free = uint64(value * (1 << 20))
		case "% Free Space":
			current.freePercent = value
		}
		disks[counter.instance] = current
	}
	result := make([]DiskMetrics, 0, len(disks))
	for name, disk := range disks {
		if disk.free == 0 || disk.freePercent <= 0 {
			continue
		}
		total := uint64(float64(disk.free) * 100 / disk.freePercent)
		used := subtract(total, disk.free)
		result = append(result, DiskMetrics{Path: name, Total: total, Used: used, Available: disk.free, UsedPercent: percentage(used, total)})
	}
	return result
}
