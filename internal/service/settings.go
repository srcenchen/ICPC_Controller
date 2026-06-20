package service

import (
	"encoding/json"
	"log"
	"sync"

	"ICPCRemoteControl/internal/data"

	"golang.org/x/crypto/bcrypt"
)

// PresetCommand is a named preset command.
type PresetCommand struct {
	Name    string `json:"name"`
	Desc    string `json:"desc"`
	Command string `json:"command"`
	Color   string `json:"color"` // button color hint: danger, warning, success, primary
}

// NetworkRule is a mihomo routing rule.
type NetworkRule struct {
	Type  string `json:"type"`  // DOMAIN, DOMAIN-SUFFIX, DOMAIN-KEYWORD
	Value string `json:"value"` // the domain, suffix, or keyword
}

// CheckinConfig holds contestant-facing check-in page configuration.
type CheckinConfig struct {
	WelcomeText     string `json:"welcome_text"`      // welcome message shown at top of checkin page
	WarningText     string `json:"warning_text"`      // warning shown on checkin form
	PostCheckinMsg  string `json:"post_checkin_msg"`  // message shown after successful checkin
	PostCheckoutCmd string `json:"post_checkout_cmd"` // command to run after checkout
	PostCheckoutMsg string `json:"post_checkout_msg"` // message shown after successful checkout
}

// ServerSettings holds mutable server configuration that can be changed via the admin UI.
// Changes are automatically persisted to the database.
type ServerSettings struct {
	mu                   sync.RWMutex
	settingsRepo         *data.SettingsRepo
	HostnamePrefix       string          `json:"hostname_prefix"`
	Presets              []PresetCommand `json:"presets"`
	NetworkRules         []NetworkRule   `json:"network_rules"`
	CheckinCfg           CheckinConfig   `json:"checkin_config"`
	ScreenMonitorEnabled bool            `json:"screen_monitor_enabled"`
}

// defaultPresets returns the built-in preset commands.
func defaultPresets() []PresetCommand {
	return []PresetCommand{
		{
			Name:    "锁定键鼠",
			Desc:    "禁止所有输入设备（键盘、鼠标）",
			Command: "mkdir -p /etc/udev/rules.d && echo 'ACTION==\"add|change\", SUBSYSTEM==\"input\", ENV{LIBINPUT_IGNORE_DEVICE}=\"1\"' > /etc/udev/rules.d/99-icpc-lock.rules && udevadm control --reload-rules && udevadm trigger --subsystem-match=input && echo '键鼠已锁定'",
			Color:   "danger",
		},
		{
			Name:    "解锁键鼠",
			Desc:    "恢复所有输入设备",
			Command: "rm -f /etc/udev/rules.d/99-icpc-lock.rules && udevadm control --reload-rules && udevadm trigger --subsystem-match=input && echo '键鼠已解锁'",
			Color:   "success",
		},
		{
			Name:    "锁屏",
			Desc:    "锁定屏幕（需要桌面环境支持）",
			Command: "loginctl lock-sessions 2>/dev/null || xdg-screensaver lock 2>/dev/null || echo '无法锁屏'",
			Color:   "warning",
		},
		{
			Name:    "解锁",
			Desc:    "解锁屏幕（需要桌面环境支持）",
			Command: "loginctl unlock-sessions 2>/dev/null || \\\nDISPLAY=:0 DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$(pgrep -u $(whoami) -x kded5 || echo 1000)/bus qdbus org.freedesktop.ScreenSaver /ScreenSaver SetActive false 2>/dev/null || \\\npkill -9 -f kscreenlocker_greet 2>/dev/null || \\\necho '无法解锁'",
			Color:   "success",
		},
		{
			Name:    "关机",
			Desc:    "立即关闭选手机",
			Command: "shutdown now",
			Color:   "danger",
		},
		{
			Name:    "重启",
			Desc:    "立即重启选手机",
			Command: "reboot",
			Color:   "warning",
		},
		{
			Name:    "同步时间",
			Desc:    "从服务器同步系统时间",
			Command: "timedatectl set-ntp true 2>/dev/null && echo 'NTP 已启用' || echo '时间同步设置失败'",
			Color:   "primary",
		},
	}
}

func defaultNetworkRules() []NetworkRule {
	return []NetworkRule{
		{Type: "DOMAIN-SUFFIX", Value: "nowcoder.com"},
		{Type: "DOMAIN-SUFFIX", Value: "aliyuncs.com"},
		{Type: "DOMAIN-SUFFIX", Value: "126.net"},
		{Type: "DOMAIN-KEYWORD", Value: "c.dun"},
	}
}

// settings DB keys for persistence.
const (
	settingKeyHostnamePrefix       = "hostname_prefix"
	settingKeyPresets              = "presets"
	settingKeyNetworkRules         = "network_rules"
	settingKeyCheckinConfig        = "checkin_config"
	settingKeyScreenMonitorEnabled = "screen_monitor_enabled"
)

// NewServerSettings creates a new ServerSettings, loading persisted values from the
// database. Falls back to built-in defaults for any keys not yet stored.
func NewServerSettings(prefix string, repo *data.SettingsRepo) *ServerSettings {
	s := &ServerSettings{
		settingsRepo:   repo,
		HostnamePrefix: prefix,
		Presets:        defaultPresets(),
		NetworkRules:   defaultNetworkRules(),
		CheckinCfg: CheckinConfig{
			WelcomeText:     "欢迎参加XCPC竞赛",
			WarningText:     "严禁场外答题，否则成绩无效！",
			PostCheckinMsg:  "签到成功",
			PostCheckoutCmd: "shutdown -h +1",
			PostCheckoutMsg: "签退成功，您的电脑将在1分钟后自动关机。",
		},
		ScreenMonitorEnabled: false,
	}

	// Load persisted values from DB, falling back to defaults.
	if repo != nil {
		s.loadFromDB()
	}

	return s
}

// loadFromDB reads persisted settings from the database, keeping defaults for any
// keys that haven't been saved yet.
func (s *ServerSettings) loadFromDB() {
	if raw, err := s.settingsRepo.Get(settingKeyHostnamePrefix); err == nil && raw != "" {
		s.HostnamePrefix = raw
	}
	if raw, err := s.settingsRepo.Get(settingKeyPresets); err == nil && raw != "" {
		var presets []PresetCommand
		if json.Unmarshal([]byte(raw), &presets) == nil {
			s.Presets = presets
		}
	}
	if raw, err := s.settingsRepo.Get(settingKeyNetworkRules); err == nil && raw != "" {
		var rules []NetworkRule
		if json.Unmarshal([]byte(raw), &rules) == nil {
			s.NetworkRules = rules
		}
	}
	if raw, err := s.settingsRepo.Get(settingKeyCheckinConfig); err == nil && raw != "" {
		var cfg CheckinConfig
		if json.Unmarshal([]byte(raw), &cfg) == nil {
			s.CheckinCfg = cfg
		}
	}
	if raw, err := s.settingsRepo.Get(settingKeyScreenMonitorEnabled); err == nil && raw != "" {
		s.ScreenMonitorEnabled = raw == "true"
	}
	log.Printf("[settings] loaded persisted settings from database")
}

// persist saves a setting key-value pair to the database. Errors are logged but not
// surfaced — in-memory state is always correct even if the DB write fails.
func (s *ServerSettings) persist(key, value string) {
	if s.settingsRepo == nil {
		return
	}
	if err := s.settingsRepo.Set(key, value); err != nil {
		log.Printf("[settings] persist %q failed: %v", key, err)
	}
}

// GetCheckinConfig returns a copy of the checkin config.
func (s *ServerSettings) GetCheckinConfig() CheckinConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CheckinCfg
}

// SetCheckinConfig updates the checkin config and persists to DB.
func (s *ServerSettings) SetCheckinConfig(cfg CheckinConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CheckinCfg = cfg
	data, _ := json.Marshal(cfg)
	s.persist(settingKeyCheckinConfig, string(data))
}

// GetHostnamePrefix returns the current hostname prefix.
func (s *ServerSettings) GetHostnamePrefix() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.HostnamePrefix
}

// SetHostnamePrefix updates the hostname prefix and persists to DB.
func (s *ServerSettings) SetHostnamePrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HostnamePrefix = prefix
	s.persist(settingKeyHostnamePrefix, prefix)
}

// GetPresets returns a copy of the current presets.
func (s *ServerSettings) GetPresets() []PresetCommand {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PresetCommand, len(s.Presets))
	copy(out, s.Presets)
	return out
}

// SetPresets replaces the presets list and persists to DB.
func (s *ServerSettings) SetPresets(presets []PresetCommand) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Presets = presets
	data, _ := json.Marshal(presets)
	s.persist(settingKeyPresets, string(data))
}

// GetNetworkRules returns a copy of the current network rules.
func (s *ServerSettings) GetNetworkRules() []NetworkRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]NetworkRule, len(s.NetworkRules))
	copy(out, s.NetworkRules)
	return out
}

// SetNetworkRules replaces the network rules list and persists to DB.
func (s *ServerSettings) SetNetworkRules(rules []NetworkRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NetworkRules = rules
	data, _ := json.Marshal(rules)
	s.persist(settingKeyNetworkRules, string(data))
}

// Snapshot returns a copy of current settings for JSON serialization.
func (s *ServerSettings) Snapshot() ServerSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	presets := make([]PresetCommand, len(s.Presets))
	copy(presets, s.Presets)
	rules := make([]NetworkRule, len(s.NetworkRules))
	copy(rules, s.NetworkRules)
	return ServerSettings{
		HostnamePrefix:       s.HostnamePrefix,
		Presets:              presets,
		NetworkRules:         rules,
		CheckinCfg:           s.CheckinCfg,
		ScreenMonitorEnabled: s.ScreenMonitorEnabled,
	}
}

// GetScreenMonitorEnabled returns the current screen monitor enabled state.
func (s *ServerSettings) GetScreenMonitorEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ScreenMonitorEnabled
}

// SetScreenMonitorEnabled updates the screen monitor enabled state and persists it to the database.
func (s *ServerSettings) SetScreenMonitorEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ScreenMonitorEnabled = enabled
	val := "false"
	if enabled {
		val = "true"
	}
	s.persist(settingKeyScreenMonitorEnabled, val)
}

// MarshalPresets serializes presets to JSON bytes.
func (s *ServerSettings) MarshalPresets() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(s.Presets)
}

// settings DB keys for password.
const settingKeyAdminPassword = "admin_password"

// VerifyPassword checks if the password matches the stored hashed password.
func (s *ServerSettings) VerifyPassword(password string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var hash string
	if s.settingsRepo != nil {
		if raw, err := s.settingsRepo.Get(settingKeyAdminPassword); err == nil && raw != "" {
			hash = raw
		}
	}

	// Fall back to default password "admin" if empty
	if hash == "" {
		defaultHash, _ := bcrypt.GenerateFromPassword([]byte("admin"), 10)
		hash = string(defaultHash)
	}

	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// SetAdminPassword hashes and persists the new password.
func (s *ServerSettings) SetAdminPassword(newPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	hashBytes, err := bcrypt.GenerateFromPassword([]byte(newPassword), 10)
	if err != nil {
		return err
	}

	s.persist(settingKeyAdminPassword, string(hashBytes))
	return nil
}
