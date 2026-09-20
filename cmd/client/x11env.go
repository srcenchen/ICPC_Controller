package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Shared X11 environment discovery for the watermark overlay and screen capture.
// Well-known display-manager paths are tried first; scanning /proc/*/environ is
// only a last resort because it is expensive and fragile. The resolved
// Xauthority is cached across reconnects and only re-discovered when the cached
// file disappears (e.g. after an X server restart).

var (
	xauthMu     sync.Mutex
	xauthCached string
)

// resolveDisplay returns the X display, preferring $DISPLAY then the socket dir.
func resolveDisplay() string {
	if disp := os.Getenv("DISPLAY"); disp != "" {
		return disp
	}
	if files, err := filepath.Glob("/tmp/.X11-unix/X*"); err == nil && len(files) > 0 {
		name := filepath.Base(files[0])
		if len(name) > 1 && name[0] == 'X' {
			return ":" + name[1:]
		}
	}
	return ":0"
}

// resolveXAuthority finds a usable Xauthority file, caching the result. The
// well-known paths cover SDDM/GDM/LightDM and normal user logins; the /proc
// scan runs only if none match.
func resolveXAuthority() string {
	xauthMu.Lock()
	defer xauthMu.Unlock()

	if xauthCached != "" {
		if fileUsable(xauthCached) {
			return xauthCached
		}
		xauthCached = "" // stale (X restarted); rediscover
	}

	if path := discoverXAuthority(); path != "" {
		xauthCached = path
		return path
	}
	return ""
}

func discoverXAuthority() string {
	// 1. Explicit env.
	if xauth := os.Getenv("XAUTHORITY"); xauth != "" && fileUsable(xauth) {
		return xauth
	}

	// 2. Well-known display-manager and user session locations.
	patterns := []string{
		"/run/sddm/*",                // SDDM (Arch/KDE default)
		"/run/user/*/gdm/Xauthority", // GDM
		"/run/user/*/xauth_*",        // generic per-user
		"/run/user/*/.mutter-Xwaylandauth.*",
		"/var/run/sddm/*",
		"/var/lib/sddm/.Xauthority",
		"/var/run/lightdm/root/*",
		"/var/lib/lightdm/.Xauthority",
		"/home/*/.Xauthority",
		"/root/.Xauthority",
	}
	for _, pat := range patterns {
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			if fileUsable(m) {
				return m
			}
		}
	}

	// 3. loginctl: map the active graphical session to its user home.
	if os.Geteuid() == 0 {
		if out, err := exec.Command("loginctl", "list-sessions", "--no-legend").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(line)
				if len(fields) < 3 {
					continue
				}
				user := fields[2]
				if user == "" || user == "root" {
					continue
				}
				cand := filepath.Join("/home", user, ".Xauthority")
				if fileUsable(cand) {
					return cand
				}
			}
		}
	}

	// 4. Last resort: scan process environments.
	if path := stealXAuthorityFromProc(); path != "" {
		log.Printf("[x11] resolved Xauthority via /proc scan: %s", path)
		return path
	}
	return ""
}

func fileUsable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// prepareX11Env sets DISPLAY/XAUTHORITY in the environment when unset, using the
// shared discovery. Used by the screen capture path.
func prepareX11Env() {
	if os.Getenv("DISPLAY") == "" {
		disp := resolveDisplay()
		os.Setenv("DISPLAY", disp)
		log.Printf("[x11] DISPLAY=%s", disp)
	}
	if os.Getenv("XAUTHORITY") == "" {
		if xauth := resolveXAuthority(); xauth != "" {
			os.Setenv("XAUTHORITY", xauth)
			log.Printf("[x11] XAUTHORITY=%s", xauth)
		}
	}
}
