package model

// Device represents a contestant machine record in the database.
type Device struct {
	ID               int64  `json:"id"`
	AssignedID       int    `json:"assigned_id"`
	MacAddress       string `json:"mac_address"`
	Hostname         string `json:"hostname"`
	Username         string `json:"username"`
	OSName           string `json:"os_name"`
	OSVersion        string `json:"os_version"`
	OSPrettyName     string `json:"os_pretty_name"`
	KernelRelease    string `json:"kernel_release"`
	KernelArch       string `json:"kernel_arch"`
	CPUModel         string `json:"cpu_model"`
	CPUPhysicalCores int    `json:"cpu_physical_cores"`
	CPULogicalCores  int    `json:"cpu_logical_cores"`
	CPUPackages      int    `json:"cpu_packages"`
	GPUInfo          string `json:"gpu_info"`
	MemoryTotal      int64  `json:"memory_total"`
	MemoryUsed       int64  `json:"memory_used"`
	DiskInfo         string `json:"disk_info"`
	LocalIP          string `json:"local_ip"`
	DEName           string `json:"de_name"`
	WMName           string `json:"wm_name"`
	Shell            string `json:"shell"`
	Terminal         string `json:"terminal"`
	DisplayInfo      string `json:"display_info"`
	Uptime           int64  `json:"uptime"`
	Packages         string `json:"packages"`
	FastfetchRaw     string `json:"fastfetch_raw"`
	Connected        bool   `json:"connected"`
	LastSeen         string `json:"last_seen"`
	FirstSeen        string `json:"first_seen"`
	UpdatedAt        string `json:"updated_at"`
	CheckinStatus    int    `json:"checkin_status"` // 0=未签到, 1=已签到, 2=已签退
	StudentName      string `json:"student_name"`
	StudentNum       string `json:"student_num"`
	CheckinTime      string `json:"checkin_time"`
	CheckoutTime     string `json:"checkout_time"`
	// Live health metrics from the client heartbeat (-1 = unknown/unsupported).
	CPUPct        float64 `json:"cpu_pct"`
	MemPct        float64 `json:"mem_pct"`
	DiskPct       float64 `json:"disk_pct"`
	TempC         float64 `json:"temp_c"`
	Load1         float64 `json:"load1"`
	HealthAt      string  `json:"health_at"`
	ClientVersion string  `json:"client_version"`
}

// DeviceSummary is a lightweight view for list endpoints.
type DeviceSummary struct {
	AssignedID    int    `json:"assigned_id"`
	Hostname      string `json:"hostname"`
	Username      string `json:"username"`
	OSName        string `json:"os_name"`
	CPUModel      string `json:"cpu_model"`
	MemoryTotal   int64  `json:"memory_total"`
	MemoryUsed    int64  `json:"memory_used"`
	LocalIP       string `json:"local_ip"`
	Connected     bool   `json:"connected"`
	LastSeen      string `json:"last_seen"`
	CheckinStatus int    `json:"checkin_status"`
	StudentName   string `json:"student_name"`
	StudentNum    string `json:"student_num"`
	// Health metrics (-1 = unknown); surfaced in the device list / dashboard.
	CPUPct   float64 `json:"cpu_pct"`
	MemPct   float64 `json:"mem_pct"`
	DiskPct  float64 `json:"disk_pct"`
	TempC    float64 `json:"temp_c"`
	Load1    float64 `json:"load1"`
	HealthAt string  `json:"health_at"`
}
