package util

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var StartTime = time.Now()

func GetUptimeFormatted() string {
	uptime := time.Since(StartTime)
	totalSec := int(uptime.Seconds())

	days := totalSec / 86400
	hours := (totalSec % 86400) / 3600
	minutes := (totalSec % 3600) / 60
	seconds := totalSec % 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 || days > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 || hours > 0 || days > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	parts = append(parts, fmt.Sprintf("%ds", seconds))

	return strings.Join(parts, " ")
}

func GetProcessRSS() string {
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "VmRSS:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, err := strconv.ParseFloat(fields[1], 64); err == nil {
						return fmt.Sprintf("%.2f MB", kb/1024.0)
					}
				}
			}
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return fmt.Sprintf("%.2f MB", float64(m.Sys)/1024.0/1024.0)
}

func GetHardwareReport() (processor, cpu, cpuLoad, gpu string) {
	numCPU := runtime.NumCPU()
	cpuModel, hardware, processor := readCPUInfo()
	maxFreqGHz := readMaxFreqGHz(numCPU)
	processor = resolveProcessor(processor, cpuModel, hardware)
	return processor, describeCPU(numCPU, cpuModel, processor, maxFreqGHz), readCPULoad(numCPU), detectGPU()
}

func readCPUInfo() (cpuModel, hardware, processor string) {
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if (key == "model name" || key == "Model Name") && cpuModel == "" {
				cpuModel = val
			} else if key == "Hardware" && hardware == "" {
				hardware = val
			} else if key == "Processor" && processor == "" {
				processor = val
			}
		}
	}

	if cpuModel == "" {
		if out, err := exec.Command("lscpu").Output(); err == nil {
			cpuModel = fieldAfter(string(out), "Model name:")
		}
	}

	if processor == "" {
		switch runtime.GOOS {
		case "darwin":
			if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
				processor = strings.TrimSpace(string(out))
			}
		case "windows":
			processor = os.Getenv("PROCESSOR_IDENTIFIER")
		}
	}
	return cpuModel, hardware, processor
}

// fieldAfter mengembalikan nilai setelah prefix pada baris pertama yang cocok.
func fieldAfter(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func readMaxFreqGHz(numCPU int) float64 {
	maxFreqGHz := 0.0
	for i := 0; i < numCPU; i++ {
		freqPath := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/cpuinfo_max_freq", i)
		fData, err := os.ReadFile(freqPath)
		if err != nil {
			continue
		}
		khz, err := strconv.ParseFloat(strings.TrimSpace(string(fData)), 64)
		if err == nil && khz/1000000.0 > maxFreqGHz {
			maxFreqGHz = khz / 1000000.0
		}
	}

	if maxFreqGHz == 0 {
		if out, err := exec.Command("lscpu").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if !strings.HasPrefix(line, "CPU max MHz:") && !strings.HasPrefix(line, "CPU MHz:") {
					continue
				}
				parts := strings.SplitN(line, ":", 2)
				if len(parts) != 2 {
					continue
				}
				if mhz, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil && mhz > 0 {
					maxFreqGHz = mhz / 1000.0
					break
				}
			}
		}
	}
	return maxFreqGHz
}

func resolveProcessor(processor, cpuModel, hardware string) string {
	if hardware != "" {
		return hardware
	}
	if processor == "" && cpuModel != "" {
		return cpuModel
	}
	if processor == "" {
		return fmt.Sprintf("%s Processor", strings.ToUpper(runtime.GOARCH))
	}
	return processor
}

func describeCPU(numCPU int, cpuModel, processor string, maxFreqGHz float64) string {
	freqStr := ""
	if maxFreqGHz > 0 && !strings.Contains(processor, "@") && !strings.Contains(cpuModel, "@") {
		freqStr = fmt.Sprintf(" @ %.2f GHz", maxFreqGHz)
	}
	if cpuModel != "" && cpuModel != processor {
		return fmt.Sprintf("%d cores (%s%s)", numCPU, cpuModel, freqStr)
	}
	return fmt.Sprintf("%d cores%s", numCPU, freqStr)
}

func detectGPU() string {
	if out, err := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output(); err == nil {
		if g := strings.TrimSpace(string(out)); g != "" {
			return strings.Split(g, "\n")[0]
		}
	}

	if out, err := exec.Command("lspci").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			lower := strings.ToLower(line)
			if !strings.Contains(lower, "vga compatible controller") &&
				!strings.Contains(lower, "3d controller") &&
				!strings.Contains(lower, "display controller") {
				continue
			}
			if parts := strings.SplitN(line, ":", 3); len(parts) == 3 {
				return strings.TrimSpace(parts[2])
			}
		}
	}
	return "None (Headless)"
}

func readCPULoad(numCPU int) string {
	if load, ok := loadAvgFormatted(); ok && !isMockPRootLoad() {
		return load
	}

	totalRatio := 0.0
	validCores := 0
	for i := 0; i < numCPU; i++ {
		curData, err1 := os.ReadFile(fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/scaling_cur_freq", i))
		maxData, err2 := os.ReadFile(fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/cpuinfo_max_freq", i))
		if err1 != nil || err2 != nil {
			continue
		}
		cur, _ := strconv.ParseFloat(strings.TrimSpace(string(curData)), 64)
		max, _ := strconv.ParseFloat(strings.TrimSpace(string(maxData)), 64)
		if max > 0 {
			totalRatio += cur / max
			validCores++
		}
	}
	if validCores > 0 {
		return fmt.Sprintf("%.1f%%", (totalRatio/float64(validCores))*100.0)
	}

	if load, ok := loadAvgFormatted(); ok {
		return load
	}
	return "N/A"
}

func loadAvgFormatted() (string, bool) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "", false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return "", false
	}
	return fmt.Sprintf("%s, %s, %s", fields[0], fields[1], fields[2]), true
}

// PRoot containers melaporkan loadavg palsu 0.00 0.00 0.00 1/1 1.
func isMockPRootLoad() bool {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data))
	return len(fields) >= 5 && fields[0] == "0.00" && fields[1] == "0.00" &&
		fields[2] == "0.00" && fields[3] == "1/1" && fields[4] == "1"
}
