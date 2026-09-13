// Package screens gathers the info shown on the front panel LCD.
package screens

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Screen returns the two 16-character rows to display.
type Screen func() (line1, line2 string)

// PrimaryIP returns this host's outbound IPv4 address, without sending any
// actual traffic (UDP "connect" only resolves a local route).
func PrimaryIP() string {
	conn, err := net.DialTimeout("udp", "8.8.8.8:80", time.Second)
	if err != nil {
		return "no network"
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "no network"
	}
	return addr.IP.String()
}

// IP is the screen showing the host's primary IP address.
func IP() (string, string) {
	return "IP ADDRESS", PrimaryIP()
}

func run(timeout time.Duration, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
		return string(out), err
	case <-time.After(timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return "", fmt.Errorf("%s: timed out", name)
	}
}

func zpoolStatus(pool string) (size, alloc, free, cap string, ok bool) {
	out, err := run(5*time.Second, "zpool", "list", "-H", "-o", "size,alloc,free,capacity", pool)
	if err != nil {
		return "", "", "", "", false
	}
	fields := strings.Split(strings.TrimSpace(out), "\t")
	if len(fields) != 4 {
		return "", "", "", "", false
	}
	return fields[0], fields[1], fields[2], fields[3], true
}

// Zpool builds a screen reporting capacity for the given ZFS pool.
func Zpool(pool string) Screen {
	return func() (string, string) {
		size, alloc, free, cap, ok := zpoolStatus(pool)
		if !ok {
			return strings.ToUpper(pool) + " POOL", "unavailable"
		}
		return fmt.Sprintf("%s: %s USED", pool, cap), fmt.Sprintf("%s/%s FREE:%s", alloc, size, free)
	}
}

func zpoolHealth(pool string) (string, bool) {
	out, err := run(5*time.Second, "zpool", "list", "-H", "-o", "health", pool)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// ZpoolHealth builds a screen reporting ZFS pool health (ONLINE, DEGRADED,
// FAULTED, ...) for the given pool — separate from Zpool's capacity screen
// since a degraded/faulted pool is the more urgent thing to notice.
func ZpoolHealth(pool string) Screen {
	return func() (string, string) {
		health, ok := zpoolHealth(pool)
		if !ok {
			return strings.ToUpper(pool) + " HEALTH", "unavailable"
		}
		return strings.ToUpper(pool) + " HEALTH", health
	}
}

// OSDiskUsage is the screen showing used/total space on the root
// filesystem — the OS/system disk (LVM on this NAS), separate from the ZFS
// data pool.
func OSDiskUsage() (string, string) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return "OS DISK", "unavailable"
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	if total == 0 {
		return "OS DISK", "unavailable"
	}
	// bytes -> "KB" units so formatUsageGB's /1024/1024 divisor (built for
	// /proc/meminfo's KB values) yields GB here too.
	return "OS DISK", formatUsageGB((total-free)/1024, total/1024)
}

// OSDiskTemp builds a screen reporting the OS/system disk's temperature via
// smartctl. Many USB-SATA bridges (this NAS's system disk is a USB-attached
// SSD) hide SMART data unless addressed with the "sat" protocol, so that's
// tried as a fallback after a plain read.
func OSDiskTemp(device string) Screen {
	return func() (string, string) {
		if _, err := exec.LookPath("smartctl"); err != nil {
			return "OS DISK TEMP", "unavailable"
		}
		if t, ok := smartctlTemp(device); ok {
			return "OS DISK TEMP", fmt.Sprintf("%d C", t)
		}
		if t, ok := smartctlTemp(device, "-d", "sat"); ok {
			return "OS DISK TEMP", fmt.Sprintf("%d C", t)
		}
		return "OS DISK TEMP", "unavailable"
	}
}

func zpoolIO(pool string) (readBps, writeBps float64, ok bool) {
	// -p keeps values as exact bytes/sec instead of human-readable units;
	// two 1s samples because the first line zpool prints is the lifetime
	// average since import, not a current rate.
	out, err := run(4*time.Second, "zpool", "iostat", "-H", "-p", pool, "1", "2")
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0, 0, false
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 7 {
		return 0, 0, false
	}
	readBw, err1 := strconv.ParseFloat(fields[5], 64)
	writeBw, err2 := strconv.ParseFloat(fields[6], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return readBw, writeBw, true
}

func formatByteRate(bytesPerSec float64) string {
	switch {
	case bytesPerSec >= 1e6:
		return fmt.Sprintf("%.1fM", bytesPerSec/1e6)
	case bytesPerSec >= 1e3:
		return fmt.Sprintf("%.0fK", bytesPerSec/1e3)
	default:
		return fmt.Sprintf("%.0fB", bytesPerSec)
	}
}

// ZpoolIO builds a screen reporting live read/write throughput for the
// given ZFS pool, separate from Zpool's capacity screen.
func ZpoolIO(pool string) Screen {
	return func() (string, string) {
		readBps, writeBps, ok := zpoolIO(pool)
		if !ok {
			return strings.ToUpper(pool) + " I/O", "unavailable"
		}
		return strings.ToUpper(pool) + " I/O", fmt.Sprintf("R:%s W:%s", formatByteRate(readBps), formatByteRate(writeBps))
	}
}

func cpuTempC() (float64, bool) {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0, false
	}
	milli, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return float64(milli) / 1000.0, true
}

// CPUTemp is the screen showing the CPU thermal zone temperature.
func CPUTemp() (string, string) {
	temp, ok := cpuTempC()
	if !ok {
		return "CPU TEMP", "unavailable"
	}
	return "CPU TEMPERATURE", fmt.Sprintf("%.1f C", temp)
}

type cpuTimes struct {
	idle, total uint64
}

func readCPUTimes() (cpuTimes, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, err
	}
	firstLine, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(firstLine)
	// "cpu  user nice system idle iowait irq softirq steal guest guest_nice"
	if len(fields) < 6 || fields[0] != "cpu" {
		return cpuTimes{}, fmt.Errorf("unexpected /proc/stat format")
	}
	var t cpuTimes
	for i, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			continue
		}
		t.total += v
		if i == 3 || i == 4 { // idle, iowait both count as non-busy
			t.idle += v
		}
	}
	return t, nil
}

// CPUUsage is the screen showing instantaneous CPU utilization, sampled
// over a short window.
func CPUUsage() (string, string) {
	before, err := readCPUTimes()
	if err != nil {
		return "CPU USAGE", "unavailable"
	}
	time.Sleep(200 * time.Millisecond)
	after, err := readCPUTimes()
	if err != nil {
		return "CPU USAGE", "unavailable"
	}
	totalDelta := after.total - before.total
	if totalDelta == 0 {
		return "CPU USAGE", "unavailable"
	}
	idleDelta := after.idle - before.idle
	usage := 100 * float64(totalDelta-idleDelta) / float64(totalDelta)
	return "CPU USAGE", fmt.Sprintf("%.1f %%", usage)
}

// RAMUsage is the screen showing memory used vs. total, from /proc/meminfo.
func RAMUsage() (string, string) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "RAM USAGE", "unavailable"
	}
	var totalKB, availKB uint64
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalKB, _ = strconv.ParseUint(fields[1], 10, 64)
		case "MemAvailable:":
			availKB, _ = strconv.ParseUint(fields[1], 10, 64)
		}
	}
	if totalKB == 0 {
		return "RAM USAGE", "unavailable"
	}
	usedKB := totalKB - availKB
	return "RAM USAGE", formatUsageGB(usedKB, totalKB)
}

// formatUsageGB renders "<used>/<total>G (<pct>%)", a fixed compact shape
// that stays within 16 columns even at 3-digit GB values (e.g. "124/498G
// (25%)" is 13 chars).
func formatUsageGB(usedKB, totalKB uint64) string {
	usedGB := math.Round(float64(usedKB) / 1024 / 1024)
	totalGB := math.Round(float64(totalKB) / 1024 / 1024)
	pct := 100 * float64(usedKB) / float64(totalKB)
	return fmt.Sprintf("%.0f/%.0fG (%.0f%%)", usedGB, totalGB, pct)
}

// SwapUsage is the screen showing swap used vs. total, from /proc/meminfo.
// Distinguishes "no swap" (none configured) from "unavailable" (couldn't read).
func SwapUsage() (string, string) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "SWAP USAGE", "unavailable"
	}
	var totalKB, freeKB uint64
	found := false
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "SwapTotal:":
			totalKB, _ = strconv.ParseUint(fields[1], 10, 64)
			found = true
		case "SwapFree:":
			freeKB, _ = strconv.ParseUint(fields[1], 10, 64)
		}
	}
	if !found {
		return "SWAP USAGE", "unavailable"
	}
	if totalKB == 0 {
		return "SWAP USAGE", "no swap"
	}
	usedKB := totalKB - freeKB
	return "SWAP USAGE", formatUsageGB(usedKB, totalKB)
}

func uptimeSeconds() (float64, bool) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0, false
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return secs, true
}

// Uptime is the screen showing how long the system has been running.
func Uptime() (string, string) {
	secs, ok := uptimeSeconds()
	if !ok {
		return "UPTIME", "unavailable"
	}
	d := time.Duration(secs) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	return "UPTIME", fmt.Sprintf("%dd %02dh %02dm", days, hours, mins)
}

// LoadAverage is the screen showing the 1/5/15 minute load averages.
func LoadAverage() (string, string) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "LOAD AVG 1/5/15m", "unavailable"
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return "LOAD AVG 1/5/15m", "unavailable"
	}
	return "LOAD AVG 1/5/15m", fmt.Sprintf("%s %s %s", fields[0], fields[1], fields[2])
}

func dockerCounts() (running, total string, ok bool) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", "", false
	}
	out, err := run(5*time.Second, "docker", "info", "--format", "{{.ContainersRunning}}/{{.Containers}}")
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(strings.TrimSpace(out), "/")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// DockerContainers is the screen showing running/total Docker containers.
// Returns "unavailable" if the docker CLI isn't installed, the daemon isn't
// reachable, or the calling user lacks permission (not root or in the
// "docker" group).
func DockerContainers() (string, string) {
	running, total, ok := dockerCounts()
	if !ok {
		return "DOCKER", "unavailable"
	}
	return "DOCKER", fmt.Sprintf("%s/%s RUNNING", running, total)
}

type netTotals struct {
	rxBytes, txBytes uint64
}

// primaryInterfaceName finds the network interface carrying PrimaryIP, so
// the network screen reports the same link the IP screen does.
func primaryInterfaceName() (string, bool) {
	ip := PrimaryIP()
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", false
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.String() == ip {
				return iface.Name, true
			}
		}
	}
	return "", false
}

func readNetTotals(iface string) (netTotals, bool) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return netTotals{}, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) != iface {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		return netTotals{rxBytes: rx, txBytes: tx}, true
	}
	return netTotals{}, false
}

func formatBitRate(bytesPerSec float64) string {
	kbps := bytesPerSec * 8 / 1000
	if kbps >= 1000 {
		return fmt.Sprintf("%.1fM", kbps/1000)
	}
	return fmt.Sprintf("%.0fK", kbps)
}

// NetworkUsage is the screen showing live download/upload rates on the
// primary network interface, sampled over a short window.
func NetworkUsage() (string, string) {
	iface, ok := primaryInterfaceName()
	if !ok {
		return "NETWORK", "unavailable"
	}
	before, ok := readNetTotals(iface)
	if !ok {
		return "NET " + iface, "unavailable"
	}
	const sampleWindow = 300 * time.Millisecond
	time.Sleep(sampleWindow)
	after, ok := readNetTotals(iface)
	if !ok {
		return "NET " + iface, "unavailable"
	}
	seconds := sampleWindow.Seconds()
	rxRate := float64(after.rxBytes-before.rxBytes) / seconds
	txRate := float64(after.txBytes-before.txBytes) / seconds
	return "NET " + iface, fmt.Sprintf("D:%s U:%s", formatBitRate(rxRate), formatBitRate(txRate))
}

var driveDevices = []string{"sda", "sdb", "sdc", "sdd", "sde", "sdf"}

type driveTemp struct {
	device string
	tempC  int
}

// smartctlTemp runs `smartctl -A [extraArgs] /dev/<device>` and extracts the
// Temperature_Celsius attribute, if present.
func smartctlTemp(device string, extraArgs ...string) (int, bool) {
	args := append([]string{"-A"}, extraArgs...)
	args = append(args, "/dev/"+device)
	out, err := run(5*time.Second, "smartctl", args...)
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "Temperature_Celsius") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 9 {
			if t, err := strconv.Atoi(fields[9]); err == nil {
				return t, true
			}
		}
		break
	}
	return 0, false
}

func driveTemps() []driveTemp {
	if _, err := exec.LookPath("smartctl"); err != nil {
		return nil
	}
	var temps []driveTemp
	for _, dev := range driveDevices {
		if t, ok := smartctlTemp(dev); ok {
			temps = append(temps, driveTemp{device: dev, tempC: t})
		}
	}
	return temps
}

// DriveTemps is the screen showing average/max temperature across drives,
// via smartctl. Returns "unavailable" if smartctl is missing or no drive
// reported a readable temperature.
func DriveTemps() (string, string) {
	temps := driveTemps()
	if len(temps) == 0 {
		return "DRIVE TEMPS", "unavailable"
	}
	sum, max := 0, temps[0].tempC
	for _, t := range temps {
		sum += t.tempC
		if t.tempC > max {
			max = t.tempC
		}
	}
	avg := int(math.Round(float64(sum) / float64(len(temps))))
	return "DRIVE TEMPS", fmt.Sprintf("AVG:%dC MAX:%dC", avg, max)
}

// HottestDrive is the screen identifying which single drive is running
// hottest, since an average/max summary alone can hide which bay to check.
func HottestDrive() (string, string) {
	temps := driveTemps()
	if len(temps) == 0 {
		return "HOTTEST DRIVE", "unavailable"
	}
	hottest := temps[0]
	for _, t := range temps {
		if t.tempC > hottest.tempC {
			hottest = t
		}
	}
	return "HOTTEST DRIVE", fmt.Sprintf("%s: %d C", hottest.device, hottest.tempC)
}
