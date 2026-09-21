package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ICPCRemoteControl/internal/data"
	"github.com/google/uuid"
)

type broadcastPublication struct {
	Snapshot *data.BroadcastSnapshot `json:"snapshot"`
	Assets   []RelayFile             `json:"assets"`
	Action   string                  `json:"action"`
	Mode     string                  `json:"mode"`
}

func validBroadcastMode(mode string) bool {
	return mode == "before" || mode == "contesting" || mode == "after"
}

func validDigest(digest string) bool {
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size && digest == strings.ToLower(digest)
}

func (federation *Federation) PublishBroadcast(writer http.ResponseWriter, request *http.Request) {
	config := federation.settings.GetDeployment()
	if config.Mode != "cloud" || federation.broadcast == nil {
		writeJSON(writer, 409, map[string]string{"error": "需要支持广播的云端模式"})
		return
	}
	var input struct {
		RoomIDs []string `json:"room_ids"`
		Action  string   `json:"action"`
		Mode    string   `json:"mode"`
	}
	if json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&input) != nil || len(input.RoomIDs) == 0 || !validBroadcastMode(input.Mode) {
		writeJSON(writer, 400, map[string]string{"error": "请选择目标机房与广播模式"})
		return
	}
	if input.Action != "sync" && input.Action != "start" && input.Action != "stop" && input.Action != "reset" {
		writeJSON(writer, 400, map[string]string{"error": "invalid broadcast action"})
		return
	}
	federation.broadcastMu.Lock()
	defer federation.broadcastMu.Unlock()
	snapshot, err := federation.broadcast.repo.Snapshot(config.NodeID)
	if err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	publication := broadcastPublication{Snapshot: snapshot, Action: input.Action, Mode: input.Mode}
	if input.Action == "sync" || input.Action == "start" {
		if err := federation.freezeBroadcastAssets(&publication); err != nil {
			writeJSON(writer, 400, map[string]string{"error": err.Error()})
			return
		}
	} else {
		snapshot.Pages, snapshot.Fonts = nil, nil
		snapshot.Config = nil
	}
	body, err := json.Marshal(publication)
	if err != nil || len(body) > 2<<20 {
		writeJSON(writer, 400, map[string]string{"error": "广播内容过大（元数据上限 2 MiB，不含素材）"})
		return
	}
	transaction, err := federation.db.BeginTx(request.Context(), nil)
	if err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	defer transaction.Rollback()
	jobs := make([]string, 0)
	seen := make(map[string]bool)
	for _, room := range input.RoomIDs {
		if seen[room] {
			continue
		}
		seen[room] = true
		var exists int
		if transaction.QueryRow(`SELECT COUNT(*) FROM cluster_rooms WHERE id=?`, room).Scan(&exists) != nil || exists != 1 {
			writeJSON(writer, 400, map[string]string{"error": "未知机房"})
			return
		}
		message := FederationMessage{Type: "request", ID: uuid.NewString(), Method: "POST", Path: "/api/broadcast/sync", Body: body}
		raw, _ := json.Marshal(message)
		if _, err := transaction.Exec(`INSERT INTO cluster_jobs(id,room_id,request,created_at) VALUES(?,?,?,?)`, message.ID, room, string(raw), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			writeJSON(writer, 500, map[string]string{"error": err.Error()})
			return
		}
		jobs = append(jobs, message.ID)
	}
	if err := transaction.Commit(); err != nil {
		writeJSON(writer, 500, map[string]string{"error": err.Error()})
		return
	}
	if federation.snapshots != nil && (input.Action == "start" || input.Action == "stop") {
		federation.snapshots.RecordCloud("broadcast", "广播 "+input.Action+" · "+input.Mode, SnapshotPayload{Rooms: input.RoomIDs, BroadcastJSON: body, BroadcastMode: input.Mode})
	}
	writeJSON(writer, 202, map[string]any{"job_ids": jobs, "revision": snapshot.Revision})
}

func (federation *Federation) freezeBroadcastAssets(publication *broadcastPublication) error {
	cache := filepath.Join(federation.broadcastDir, "releases")
	if err := os.MkdirAll(cache, 0755); err != nil {
		return err
	}
	names := make(map[string]string)
	var total int64
	freeze := func(kind, name string) (string, error) {
		key := kind + "/" + name
		if frozen, exists := names[key]; exists {
			return frozen, nil
		}
		if !validBroadcastAssetName(kind, name) {
			return "", fmt.Errorf("非法广播素材：%s", key)
		}
		path := filepath.Join(federation.broadcastDir, kind, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxImageSize {
			return "", fmt.Errorf("广播素材缺失或超限：%s", key)
		}
		total += info.Size()
		if total > 512<<20 {
			return "", fmt.Errorf("广播素材总大小超过 512 MiB")
		}
		input, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer input.Close()
		output, err := os.CreateTemp(cache, ".freeze-*")
		if err != nil {
			return "", err
		}
		defer os.Remove(output.Name())
		defer output.Close()
		hasher := sha256.New()
		size, err := io.Copy(io.MultiWriter(output, hasher), io.LimitReader(input, maxImageSize+1))
		if err != nil || size != info.Size() {
			return "", fmt.Errorf("广播素材复制失败：%s", key)
		}
		if err := output.Sync(); err != nil {
			return "", err
		}
		digest := hex.EncodeToString(hasher.Sum(nil))
		if err := os.Rename(output.Name(), filepath.Join(cache, digest)); err != nil {
			return "", err
		}
		name = digest + strings.ToLower(filepath.Ext(name))
		names[key] = name
		publication.Assets = append(publication.Assets, RelayFile{Name: kind + "/" + name, SHA256: digest, Size: size})
		return name, nil
	}
	for index := range publication.Snapshot.Fonts {
		font := &publication.Snapshot.Fonts[index]
		name, err := freeze("fonts", font.Filename)
		if err != nil {
			return err
		}
		if publication.Snapshot.Config["active_font"] == font.Filename {
			publication.Snapshot.Config["active_font"] = name
		}
		font.Filename = name
	}
	fonts := publication.Snapshot.Fonts[:0]
	seenFonts := make(map[string]bool)
	for _, font := range publication.Snapshot.Fonts {
		if !seenFonts[font.Filename] {
			fonts = append(fonts, font)
			seenFonts[font.Filename] = true
		}
	}
	publication.Snapshot.Fonts = fonts
	for pageIndex := range publication.Snapshot.Pages {
		for itemIndex := range publication.Snapshot.Pages[pageIndex].Items {
			item := &publication.Snapshot.Pages[pageIndex].Items[itemIndex]
			if item.ItemType != "image" || item.Content == "" {
				continue
			}
			if !strings.HasPrefix(item.Content, "/broadcast/images/") {
				return fmt.Errorf("请先上传广播图片，不能分发外部图片地址")
			}
			name, err := freeze("images", strings.TrimPrefix(item.Content, "/broadcast/images/"))
			if err != nil {
				return err
			}
			item.Content = "/broadcast/images/" + name
		}
	}
	return nil
}

func validBroadcastAssetName(kind, name string) bool {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	extension := strings.ToLower(filepath.Ext(name))
	return (kind == "fonts" && allowedFontExts[extension] != "") || (kind == "images" && allowedImageExts[extension])
}

func (federation *Federation) ServeBroadcastAsset(writer http.ResponseWriter, request *http.Request) {
	digest := strings.TrimPrefix(request.URL.Path, "/api/cluster/broadcast-assets/")
	if request.Method != "GET" || !validDigest(digest) {
		http.Error(writer, "invalid asset request", 400)
		return
	}
	path := filepath.Join(federation.broadcastDir, "releases", digest)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(writer, request)
		return
	}
	http.ServeFile(writer, request, path)
}

func (federation *Federation) applyBroadcast(ctx context.Context, config DeploymentConfig, request FederationMessage) FederationMessage {
	response := FederationMessage{Type: "response", ID: request.ID, Status: 400, ContentType: "application/json"}
	fail := func(status int, err error) FederationMessage {
		response.Status = status
		response.Body, _ = json.Marshal(map[string]string{"error": err.Error()})
		return response
	}
	var publication broadcastPublication
	if err := json.Unmarshal(request.Body, &publication); err != nil || publication.Snapshot == nil || federation.broadcast == nil {
		return fail(400, fmt.Errorf("invalid broadcast snapshot"))
	}
	if _, err := uuid.Parse(publication.Snapshot.Source); err != nil || publication.Snapshot.Revision <= 0 || !validBroadcastMode(publication.Mode) {
		return fail(400, fmt.Errorf("invalid broadcast revision or mode"))
	}
	if publication.Action != "sync" && publication.Action != "start" && publication.Action != "stop" && publication.Action != "reset" {
		return fail(400, fmt.Errorf("invalid broadcast action"))
	}
	if federation.backup != nil {
		federation.backup.assets.RLock()
		defer federation.backup.assets.RUnlock()
	}
	federation.broadcastMu.Lock()
	defer federation.broadcastMu.Unlock()
	previous, err := federation.broadcast.repo.GetConfig("cloud_applied_revision_" + publication.Snapshot.Source)
	if err != nil {
		return fail(500, err)
	}
	revision, _ := strconv.ParseInt(previous, 10, 64)
	if revision >= publication.Snapshot.Revision {
		response.Status = 200
		response.Body = []byte(`{"applied":false,"message":"已应用相同或更新的广播版本"}`)
		return response
	}
	var total int64
	assets := make(map[string]bool)
	for _, asset := range publication.Assets {
		kind, name, found := strings.Cut(asset.Name, "/")
		total += asset.Size
		if !found || !validBroadcastAssetName(kind, name) || !validDigest(asset.SHA256) || strings.TrimSuffix(name, filepath.Ext(name)) != asset.SHA256 || asset.Size < 0 || asset.Size > maxImageSize || total > 512<<20 {
			return fail(400, fmt.Errorf("非法广播素材清单"))
		}
		assets["/broadcast/"+asset.Name] = true
	}
	for _, page := range publication.Snapshot.Pages {
		if !validBroadcastMode(page.Mode) {
			return fail(400, fmt.Errorf("invalid page mode"))
		}
		for _, item := range page.Items {
			if item.ItemType == "image" && item.Content != "" && !assets[item.Content] {
				return fail(400, fmt.Errorf("图片缺少校验清单"))
			}
		}
	}
	for _, font := range publication.Snapshot.Fonts {
		if !assets["/broadcast/fonts/"+font.Filename] {
			return fail(400, fmt.Errorf("字体缺少校验清单"))
		}
	}
	if active := publication.Snapshot.Config["active_font"]; active != "" && !assets["/broadcast/fonts/"+active] {
		return fail(400, fmt.Errorf("当前字体缺少校验清单"))
	}
	baseURL, err := federation.broadcast.repo.GetConfig("base_url")
	if err != nil {
		return fail(500, err)
	}
	if baseURL == "" {
		baseURL = "http://icpc-server.local:8082"
	}
	if publication.Action == "start" {
		parsed, err := url.Parse(baseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fail(400, fmt.Errorf("请在机房本地配置有效的广播推送地址"))
		}
	}
	for _, asset := range publication.Assets {
		path := filepath.Join(federation.broadcastDir, filepath.FromSlash(asset.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fail(500, err)
		}
		if err := pullVerifiedFile(ctx, config, asset, path, "/api/cluster/broadcast-assets/"+asset.SHA256); err != nil {
			return fail(503, err)
		}
	}
	if federation.settings.GetDeployment() != config {
		return fail(409, fmt.Errorf("部署配置已改变，广播未应用，请核实后重新发布"))
	}
	applied, err := federation.broadcast.repo.ApplySnapshot(publication.Snapshot, publication.Action, publication.Mode)
	if err != nil {
		return fail(500, err)
	}
	if applied {
		federation.broadcast.pushToAllModes()
		if publication.Action == "start" || publication.Action == "reset" {
			federation.broadcast.pushSyncReset(publication.Mode)
		}
		command := ""
		if publication.Action == "start" {
			address := strings.TrimRight(baseURL, "/") + "/broadcast/" + publication.Mode
			command = "full-firefox kill; full-firefox '" + strings.ReplaceAll(address, "'", "'\"'\"'") + "'"
		} else if publication.Action == "stop" {
			command = "full-firefox kill"
		}
		if command != "" {
			body, _ := json.Marshal(ExecuteRequest{TargetType: "broadcast", Command: command})
			result := federation.localRequest(ctx, FederationMessage{ID: request.ID, Method: "POST", Path: "/api/commands", Body: body})
			if result.Status >= 400 {
				return fail(409, fmt.Errorf("广播已同步，但设备指令失败：%s；请在机房核实", result.Body))
			}
		}
	}
	response.Status = 200
	response.Body, _ = json.Marshal(map[string]any{"applied": applied, "revision": publication.Snapshot.Revision, "action": publication.Action, "message": "素材与内容已校验；设备执行结果请查看机房命令记录"})
	return response
}
