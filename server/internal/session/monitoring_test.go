package session

import (
	"encoding/csv"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSplitSectionsAcceptsCmdMarkerWhitespace(t *testing.T) {
	sections := splitSections("__SYSTEM__ \r\nvalue\r\n__CPU__ \r\n12.5\r\n")
	if sections["SYSTEM"] != "value" || sections["CPU"] != "12.5" {
		t.Fatalf("unexpected sections: %#v", sections)
	}
}

func TestNumericValuesReadsNullSeparatedBirthTimes(t *testing.T) {
	values := numericValues([]byte("1794243660\x001794243661\x00"))
	if len(values) != 2 || values[0] != 1794243660 || values[1] != 1794243661 {
		t.Fatalf("unexpected values: %#v", values)
	}
}

func TestParseDarwinUptimeAndLoad(t *testing.T) {
	bootSeconds := time.Now().Unix() - 120
	output := "{ sec = 1794000000, usec = 0 } Mon Nov 10 00:00:00 2026\n{ 1.25 1.50 1.75 }\n"
	output = strings.Replace(output, "1794000000", strconv.FormatInt(bootSeconds, 10), 1)
	if uptime := parseDarwinUptime(output); uptime < 119 || uptime > 125 {
		t.Fatalf("unexpected uptime: %v", uptime)
	}
	load := parseDarwinLoad(output)
	if len(load) != 3 || load[0] != 1.25 || load[1] != 1.5 || load[2] != 1.75 {
		t.Fatalf("unexpected load: %#v", load)
	}
}

func TestParseDarwinMemoryAndSwap(t *testing.T) {
	memory := parseDarwinMemory("34359738368\nMach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 100.\nPages inactive: 50.\nPages speculative: 25.\n")
	available := uint64(175 * 16384)
	if memory.Available != available || memory.Used != 34359738368-available {
		t.Fatalf("unexpected memory: %#v", memory)
	}
	swap := parseDarwinSwap("total = 2048.00M  used = 512.50M  free = 1535.50M  (encrypted)")
	if swap.Total != 2048*(1<<20) || swap.Used != uint64(512.5*(1<<20)) {
		t.Fatalf("unexpected swap: %#v", swap)
	}
}

func TestParseDarwinNetworkAndDisks(t *testing.T) {
	network := parseDarwinNetwork("Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll\n" +
		"lo0 16384 <Link#1> 1 0 100 1 0 100 0\n" +
		"en0 1500 <Link#2> aa:bb 1 0 200 1 0 300 0\n")
	if len(network) != 1 || network["en0"].rx != 200 || network["en0"].tx != 300 {
		t.Fatalf("unexpected network: %#v", network)
	}
	disks := parseDarwinDF("Filesystem 1024-blocks Used Available Capacity iused ifree %iused Mounted on\n" +
		"/dev/disk3s1s1 1000 250 750 25% 1 1 0% /\n")
	if len(disks) != 1 || disks[0].Total != 1000*1024 || disks[0].Used != 250*1024 || disks[0].Path != "/" {
		t.Fatalf("unexpected disks: %#v", disks)
	}
}

func TestParseWindowsCounters(t *testing.T) {
	tests := []struct {
		header   string
		object   string
		instance string
		counter  string
	}{
		{`\\SOCAT\Network Interface(Intel[R] Wi-Fi 6 AX201 160MHz)\Bytes Received/sec`, "Network Interface", "Intel[R] Wi-Fi 6 AX201 160MHz", "Bytes Received/sec"},
		{`\\SOCAT\System\System Up Time`, "System", "", "System Up Time"},
		{`\\SOCAT\Memory\Available Bytes`, "Memory", "", "Available Bytes"},
		{`\\SOCAT\Process(powershell#10)\ID Process`, "Process", "powershell#10", "ID Process"},
	}
	for _, test := range tests {
		counter, ok := parseWindowsCounter(test.header)
		if !ok || counter.object != test.object || counter.instance != test.instance || counter.counter != test.counter {
			t.Errorf("parse %q: got %#v, %v", test.header, counter, ok)
		}
	}
}

func TestParseCmdSystem(t *testing.T) {
	values := make([]string, 25)
	values[0] = "SOCAT"
	values[1] = "Microsoft Windows 11 Home China"
	values[2] = "10.0.26300 N/A Build 26300"
	values[22] = "65,244 MB"
	values[24] = "86,066 MB"
	var output strings.Builder
	writer := csv.NewWriter(&output)
	_ = writer.Write(make([]string, len(values)))
	_ = writer.Write(values)
	writer.Flush()
	result, err := parseCmdSystem(output.String(), "16\nAMD64\n", "ProcessorNameString    REG_SZ    12th Gen Intel(R) Core(TM) i7-12650H")
	if err != nil {
		t.Fatal(err)
	}
	if result.System.Hostname != "SOCAT" || result.System.CPUCores != 16 || result.System.Architecture != "AMD64" || result.System.CPUModel != "12th Gen Intel(R) Core(TM) i7-12650H" {
		t.Fatalf("unexpected system: %#v", result.System)
	}
	if result.Memory.Total != 65244*(1<<20) || result.Swap.Total != (86066-65244)*(1<<20) {
		t.Fatalf("unexpected memory: %#v %#v", result.Memory, result.Swap)
	}
}

func TestParseCmdDisksFiltersSystemVolumes(t *testing.T) {
	disks := parseCmdDisks(map[windowsCounter]float64{
		{object: "LogicalDisk", instance: "C:", counter: "Free Megabytes"}:              100,
		{object: "LogicalDisk", instance: "C:", counter: "% Free Space"}:                50,
		{object: "LogicalDisk", instance: "HarddiskVolume2", counter: "Free Megabytes"}: 50,
		{object: "LogicalDisk", instance: "HarddiskVolume2", counter: "% Free Space"}:   50,
	})
	if len(disks) != 1 || disks[0].Path != "C:" || disks[0].Total != 200*(1<<20) || disks[0].Used != 100*(1<<20) {
		t.Fatalf("unexpected disks: %#v", disks)
	}
}

func TestLocalProfileUsesLoopbackAndCurrentUser(t *testing.T) {
	profile, err := localProfile(LocalProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != LocalProfileID || profile.Options.Host != "127.0.0.1" || profile.Options.Port != 22 || profile.Options.User == "" || profile.Options.Auth != "publicKey" {
		t.Fatalf("unexpected local profile: %#v", profile)
	}
}
