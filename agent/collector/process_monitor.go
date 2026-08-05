package collector

import (
	"sort"
	"github.com/shirou/gopsutil/v3/process"
)

type ProcessMonitor struct{}

func (p *ProcessMonitor) Name() string {
	return "ProcessMonitor"
}

func (p *ProcessMonitor) Start() error {
	return nil
}

func (p *ProcessMonitor) Stop() error {
	return nil
}

type ProcessData struct {
	PID     int32   `json:"pid"`
	Name    string  `json:"name"`
	CPU     float64 `json:"cpu"`
	Memory  float32 `json:"memory"`
}

func (p *ProcessMonitor) Collect() (map[string]interface{}, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}

	var data []ProcessData
	for _, proc := range procs {
		name, err := proc.Name()
		if err != nil {
			continue // Skip if we can't read name
		}
		cpu, err := proc.CPUPercent()
		if err != nil {
			continue
		}
		mem, err := proc.MemoryPercent()
		if err != nil {
			continue
		}

		// Only include processes using some CPU to filter out noise
		if cpu > 0.1 || mem > 1.0 {
			data = append(data, ProcessData{
				PID:    proc.Pid,
				Name:   name,
				CPU:    cpu,
				Memory: mem,
			})
		}
	}

	// Sort by CPU usage descending
	sort.Slice(data, func(i, j int) bool {
		return data[i].CPU > data[j].CPU
	})

	// Return top 5
	if len(data) > 5 {
		data = data[:5]
	}

	return map[string]interface{}{
		"top_processes": data,
	}, nil
}
