package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FastFetchEntry is one element of the fastfetch --format json array.
type FastFetchEntry struct {
	Type   string          `json:"type"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// FFOSResult holds OS information from fastfetch.
type FFOSResult struct {
	Name        string `json:"name"`
	PrettyName  string `json:"prettyName"`
	Version     string `json:"version"`
	VersionID   string `json:"versionID"`
	ID          string `json:"id"`
	Codename    string `json:"codename"`
}

// FFCPUResult holds CPU information from fastfetch.
type FFCPUResult struct {
	CPU      string `json:"cpu"`
	Vendor   string `json:"vendor"`
	Packages int    `json:"packages"`
	Cores    struct {
		Physical int `json:"physical"`
		Logical  int `json:"logical"`
		Online   int `json:"online"`
	} `json:"cores"`
}

// FFMemoryResult holds memory information from fastfetch.
type FFMemoryResult struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

// FFGPUEntry holds GPU information from fastfetch.
type FFGPUEntry struct {
	Name   string `json:"name"`
	Vendor string `json:"vendor"`
	Driver string `json:"driver"`
	Type   string `json:"type"`
}

// FFDiskEntry holds disk information from fastfetch.
type FFDiskEntry struct {
	Bytes struct {
		Available int64 `json:"available"`
		Free      int64 `json:"free"`
		Total     int64 `json:"total"`
		Used      int64 `json:"used"`
	} `json:"bytes"`
	Filesystem string `json:"filesystem"`
	Mountpoint string `json:"mountpoint"`
}

// FFLocalIPEntry holds network interface information from fastfetch.
type FFLocalIPEntry struct {
	Name         string `json:"name"`
	DefaultRoute bool   `json:"defaultRoute"`
	IPv4         string `json:"ipv4"`
}

// FFKernelResult holds kernel information from fastfetch.
type FFKernelResult struct {
	Architecture string `json:"architecture"`
	Name         string `json:"name"`
	Release      string `json:"release"`
	Version      string `json:"version"`
}

// FFHostResult holds host/machine information from fastfetch.
type FFHostResult struct {
	Name    string `json:"name"`
	Vendor  string `json:"vendor"`
	Version string `json:"version"`
}

// FFTitleResult holds user/hostname from fastfetch.
type FFTitleResult struct {
	UserName  string `json:"userName"`
	HostName  string `json:"hostName"`
	UserShell string `json:"userShell"`
}

// FFDEResult holds desktop environment information from fastfetch.
type FFDEResult struct {
	PrettyName string `json:"prettyName"`
	Version    string `json:"version"`
}

// FFWMResult holds window manager information from fastfetch.
type FFWMResult struct {
	PrettyName   string `json:"prettyName"`
	ProtocolName string `json:"protocolName"`
}

// FFShellResult holds shell information from fastfetch.
type FFShellResult struct {
	PrettyName string `json:"prettyName"`
	Version    string `json:"version"`
}

// FFTerminalResult holds terminal information from fastfetch.
type FFTerminalResult struct {
	PrettyName string `json:"prettyName"`
	Version    string `json:"version"`
}

// FFUptimeResult holds uptime from fastfetch.
type FFUptimeResult struct {
	Uptime int64 `json:"uptime"`
}

// FFDisplayEntry holds display information from fastfetch.
type FFDisplayEntry struct {
	Name   string `json:"name"`
	Output struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"output"`
}

// ParseFastFetch parses the raw fastfetch JSON and fills a Device struct.
func ParseFastFetch(raw []byte) (*Device, error) {
	var entries []FastFetchEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("unmarshal fastfetch array: %w", err)
	}

	d := &Device{}
	d.FastfetchRaw = string(raw)

	for _, entry := range entries {
		if entry.Result == nil {
			continue
		}
		switch entry.Type {
		case "Title":
			var t FFTitleResult
			if json.Unmarshal(entry.Result, &t) == nil {
				d.Username = t.UserName
				d.Hostname = t.HostName
				d.Shell = t.UserShell
			}
		case "OS":
			var o FFOSResult
			if json.Unmarshal(entry.Result, &o) == nil {
				d.OSName = o.Name
				d.OSVersion = o.Version
				d.OSPrettyName = o.PrettyName
			}
		case "Host":
			var h FFHostResult
			if json.Unmarshal(entry.Result, &h) == nil {
				// Keep hostname from Title, but supplement with host info
			}
		case "Kernel":
			var k FFKernelResult
			if json.Unmarshal(entry.Result, &k) == nil {
				d.KernelRelease = k.Release
				d.KernelArch = k.Architecture
			}
		case "CPU":
			var c FFCPUResult
			if json.Unmarshal(entry.Result, &c) == nil {
				d.CPUModel = c.CPU
				d.CPUPhysicalCores = c.Cores.Physical
				d.CPULogicalCores = c.Cores.Logical
				d.CPUPackages = c.Packages
			}
		case "GPU":
			var gpus []FFGPUEntry
			if json.Unmarshal(entry.Result, &gpus) == nil {
				gpuJSON, _ := json.Marshal(gpus)
				d.GPUInfo = string(gpuJSON)
			}
		case "Memory":
			var m FFMemoryResult
			if json.Unmarshal(entry.Result, &m) == nil {
				d.MemoryTotal = m.Total
				d.MemoryUsed = m.Used
			}
		case "Disk":
			var disks []FFDiskEntry
			if json.Unmarshal(entry.Result, &disks) == nil {
				diskJSON, _ := json.Marshal(disks)
				d.DiskInfo = string(diskJSON)
			}
		case "LocalIp":
			var ips []FFLocalIPEntry
			if json.Unmarshal(entry.Result, &ips) == nil {
				for i, ipEntry := range ips {
					if idx := strings.Index(ipEntry.IPv4, "/"); idx != -1 {
						ips[i].IPv4 = ipEntry.IPv4[:idx]
					}
				}
				ipJSON, _ := json.Marshal(ips)
				d.LocalIP = string(ipJSON)
			}
		case "DE":
			var de FFDEResult
			if json.Unmarshal(entry.Result, &de) == nil {
				d.DEName = de.PrettyName
			}
		case "WM":
			var wm FFWMResult
			if json.Unmarshal(entry.Result, &wm) == nil {
				d.WMName = wm.PrettyName
			}
		case "Shell":
			var s FFShellResult
			if json.Unmarshal(entry.Result, &s) == nil {
				d.Shell = s.PrettyName
			}
		case "Terminal":
			var term FFTerminalResult
			if json.Unmarshal(entry.Result, &term) == nil {
				d.Terminal = term.PrettyName
			}
		case "Uptime":
			var u FFUptimeResult
			if json.Unmarshal(entry.Result, &u) == nil {
				d.Uptime = u.Uptime
			} else {
				var secs int64
				if json.Unmarshal(entry.Result, &secs) == nil {
					d.Uptime = secs
				}
			}
		case "Display":
			var displays []FFDisplayEntry
			if json.Unmarshal(entry.Result, &displays) == nil {
				dispJSON, _ := json.Marshal(displays)
				d.DisplayInfo = string(dispJSON)
			}
		case "Packages":
			var pkg map[string]interface{}
			if json.Unmarshal(entry.Result, &pkg) == nil {
				pkgJSON, _ := json.Marshal(pkg)
				d.Packages = string(pkgJSON)
			}
		}
	}
	return d, nil
}
