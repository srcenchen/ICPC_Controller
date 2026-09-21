package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

type DeploymentConfig struct {
	Mode          string `json:"mode"`
	NodeID        string `json:"node_id"`
	RoomName      string `json:"room_name"`
	CloudURL      string `json:"cloud_url"`
	Token         string `json:"token,omitempty"`
	TokenSet      bool   `json:"token_set"`
	AllowInsecure bool   `json:"allow_insecure"`
	DeviceIDStart int    `json:"device_id_start"`
	AdvertiseIP   string `json:"advertise_ip"`
}

func (settings *ServerSettings) GetDeployment() DeploymentConfig {
	settings.mu.RLock()
	defer settings.mu.RUnlock()
	return settings.deployment
}

func (settings *ServerSettings) SetDeployment(cfg DeploymentConfig) error {
	settings.mu.Lock()
	defer settings.mu.Unlock()
	cfg.NodeID = settings.deployment.NodeID
	if cfg.Token == "" {
		cfg.Token = settings.deployment.Token
	}
	cfg.TokenSet = cfg.Token != ""
	cfg.RoomName = strings.TrimSpace(cfg.RoomName)
	cfg.CloudURL = strings.TrimRight(strings.TrimSpace(cfg.CloudURL), "/")
	if cfg.Mode != "standalone" && cfg.Mode != "relay" && cfg.Mode != "cloud" {
		return fmt.Errorf("模式必须是 standalone、relay 或 cloud")
	}
	if cfg.DeviceIDStart < 1 || cfg.DeviceIDStart > 1000000 {
		return fmt.Errorf("起始设备号须在 1 到 1000000 之间")
	}
	if cfg.Mode != "standalone" && len(cfg.Token) < 32 {
		return fmt.Errorf("连接密钥至少需要 32 个字符")
	}
	if cfg.Mode == "relay" {
		if cfg.RoomName == "" || len(cfg.RoomName) > 100 {
			return fmt.Errorf("请填写机房名（最多 100 字节）")
		}
		parsed, err := url.Parse(cfg.CloudURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return fmt.Errorf("云端地址必须是完整的 HTTP(S) 根地址")
		}
		if parsed.Scheme != "https" && !cfg.AllowInsecure {
			return fmt.Errorf("请使用 HTTPS；仅可信内网可显式允许 HTTP")
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if settings.settingsRepo != nil {
		if err := settings.settingsRepo.SetMany(map[string]string{"deployment": string(raw), "device_id_start": fmt.Sprint(cfg.DeviceIDStart)}); err != nil {
			return err
		}
	}
	settings.deployment = cfg
	return nil
}

func (settings *ServerSettings) loadDeployment() {
	settings.deployment = DeploymentConfig{Mode: "standalone", NodeID: uuid.NewString(), DeviceIDStart: 1}
	if settings.settingsRepo != nil {
		if raw, err := settings.settingsRepo.Get("deployment"); err == nil && raw != "" {
			_ = json.Unmarshal([]byte(raw), &settings.deployment)
		}
		if settings.deployment.NodeID == "" {
			settings.deployment.NodeID = uuid.NewString()
		}
		raw, _ := json.Marshal(settings.deployment)
		settings.persist("deployment", string(raw))
	}
}
