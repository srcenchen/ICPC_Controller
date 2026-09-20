package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// handleUpdateClient downloads a new client binary, verifies it, atomically
// swaps it into place, and restarts via systemd. A checksum guard avoids
// re-applying an identical binary (which would loop through restarts).
func handleUpdateClient(url string) {
	log.Printf("[update] update requested from %s", url)

	self, err := os.Executable()
	if err != nil {
		log.Printf("[update] cannot locate self: %v", err)
		return
	}
	self, _ = filepath.EvalSymlinks(self)

	tmp, err := downloadTo(url, filepath.Dir(self))
	if err != nil {
		log.Printf("[update] download failed: %v", err)
		return
	}
	defer os.Remove(tmp)

	newSum, err := fileSHA256(tmp)
	if err != nil {
		log.Printf("[update] checksum new binary: %v", err)
		return
	}
	if curSum, err := fileSHA256(self); err == nil && curSum == newSum {
		log.Printf("[update] already up to date (sha256 %s); skipping", newSum[:12])
		return
	}

	if err := os.Chmod(tmp, 0o755); err != nil {
		log.Printf("[update] chmod: %v", err)
		return
	}
	// Atomic replace: rename within the same directory.
	if err := os.Rename(tmp, self); err != nil {
		log.Printf("[update] swap binary: %v", err)
		return
	}
	log.Printf("[update] binary replaced (sha256 %s), restarting", newSum[:12])

	// Prefer systemd restart; fall back to exiting so a Restart=always unit or
	// supervisor brings the new binary up.
	if _, err := exec.LookPath("systemctl"); err == nil {
		if err := exec.Command("systemctl", "restart", "icpc-client.service").Start(); err != nil {
			log.Printf("[update] systemctl restart failed: %v; exiting to let supervisor restart", err)
			os.Exit(0)
		}
		return
	}
	log.Printf("[update] no systemctl; exiting for supervisor restart")
	os.Exit(0)
}

func downloadTo(url, dir string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(dir, ".icpc-client-update-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
