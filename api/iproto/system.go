package iproto

// SSystemConfig reports the currently applied worker pool size and local system metrics.
type SSystemConfig struct {
	PoolSize          int           `json:"pool_size"`
	CPUCores          *int          `json:"cpu_cores"`
	CPUUsagePercent   *float64      `json:"cpu_usage_percent"`
	MemoryTotalBytes  *uint64       `json:"memory_total_bytes"`
	MemoryUsedPercent *float64      `json:"memory_used_percent"`
	Disks             []SSystemDisk `json:"disks"`
}

type SSystemDisk struct {
	Device      string   `json:"device"`
	Mountpoint  string   `json:"mountpoint"`
	FSType      string   `json:"fs_type"`
	TotalBytes  *uint64  `json:"total_bytes"`
	UsedBytes   *uint64  `json:"used_bytes"`
	UsedPercent *float64 `json:"used_percent"`
}
