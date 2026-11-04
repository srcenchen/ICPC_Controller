package service

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/judgebridge"
)

// JudgeBridge keeps the shared secret server-side. Clients only relay signed
// challenges over their current, registered TCP connection.
type JudgeBridge struct {
	db       *sql.DB
	devices  *data.DeviceRepo
	hub      *biz.Hub
	settings *ServerSettings
	secret   string
	origins  map[string]bool
}

func NewJudgeBridge(db *sql.DB, devices *data.DeviceRepo, hub *biz.Hub, settings *ServerSettings, secret string, origins []string) (*JudgeBridge, error) {
	bridge := &JudgeBridge{db: db, devices: devices, hub: hub, settings: settings, secret: secret, origins: make(map[string]bool)}
	for _, origin := range origins {
		origin = strings.TrimRight(strings.TrimSpace(origin), "/")
		u, err := url.Parse(origin)
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" {
			bridge.origins[origin] = true
		}
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS judge_presence_receipt (
		nonce TEXT PRIMARY KEY, challenge_hash TEXT NOT NULL, device_id INTEGER NOT NULL,
		proof TEXT NOT NULL, expires_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS judge_attendance (
		event_id TEXT PRIMARY KEY, contest_id INTEGER NOT NULL, student_number TEXT NOT NULL,
		student_name TEXT NOT NULL, access_kind TEXT NOT NULL, node_id TEXT NOT NULL DEFAULT '',
		device_id INTEGER NOT NULL DEFAULT 0, logged_in_at INTEGER NOT NULL,
		received_at INTEGER NOT NULL, content_hash TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS judge_attendance_contest ON judge_attendance(contest_id,logged_in_at);`)
	return bridge, err
}

var judgeStudentNumber = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,59}$`)
var judgeNonce = regexp.MustCompile(`^[a-f0-9]{64}$`)
var judgeEventID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func validJudgeIdentity(claims judgebridge.Claims) bool {
	return claims.CID > 0 && judgeStudentNumber.MatchString(claims.StudentNumber) &&
		utf8.ValidString(claims.StudentName) && utf8.RuneCountInString(claims.StudentName) > 0 &&
		utf8.RuneCountInString(claims.StudentName) <= 128 && !strings.ContainsAny(claims.StudentName, "\x00\r\n|")
}

func (bridge *JudgeBridge) Proof(client *biz.ClientConn, token, origin string) (string, error) {
	if bridge == nil || len(bridge.secret) < 32 || len(bridge.origins) == 0 {
		return "", errors.New("集控尚未配置 OJ 共享密钥与允许的 OJ 地址")
	}
	claims, err := judgebridge.Verify(token, bridge.secret, "cwxu-challenge", time.Now())
	if err != nil {
		return "", err
	}
	if !validJudgeIdentity(claims) || claims.UID <= 0 || !judgeNonce.MatchString(claims.Nonce) ||
		!judgeNonce.MatchString(claims.SessionHash) || claims.Origin != origin || !bridge.origins[origin] ||
		(claims.Purpose != "login" && claims.Purpose != "submit") {
		return "", errors.New("OJ 验证请求身份、来源或用途无效")
	}
	if client == nil || bridge.hub.GetClient(client.AssignedID) != client {
		return "", errors.New("当前客户端连接已失效，请重新连接集控")
	}
	device, err := bridge.devices.GetByAssignedID(client.AssignedID)
	if err != nil || !device.Connected {
		return "", errors.New("集控未确认此设备在线")
	}
	hash := sha256.Sum256([]byte(token))
	challengeHash := hex.EncodeToString(hash[:])
	tx, err := bridge.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var previousHash, previousProof string
	var previousDevice int
	var previousExpiry int64
	err = tx.QueryRow(`SELECT challenge_hash,device_id,proof,expires_at FROM judge_presence_receipt WHERE nonce=?`, claims.Nonce).
		Scan(&previousHash, &previousDevice, &previousProof, &previousExpiry)
	if err == nil {
		if previousHash != challengeHash || previousDevice != client.AssignedID || previousExpiry <= time.Now().Unix() {
			return "", errors.New("验证请求已用于其他设备或已过期")
		}
		return previousProof, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if claims.Purpose == "submit" {
		if device.CheckinStatus != 1 || device.StudentNum != claims.StudentNumber || device.StudentName != claims.StudentName {
			return "", errors.New("本机签到身份与比赛账号不一致，请重新登录比赛")
		}
	} else {
		// Repeated logins for the same identity retain the original check-in time.
		now := time.Now().Format(time.RFC3339)
		result, err := tx.Exec(`UPDATE devices SET checkin_time=CASE WHEN checkin_status=1 AND student_num=? AND student_name=? THEN checkin_time ELSE ? END,
			checkin_status=1,student_name=?,student_num=?,checkout_time='',updated_at=? WHERE assigned_id=? AND connected=1`,
			claims.StudentNumber, claims.StudentName, now, claims.StudentName, claims.StudentNumber, now, client.AssignedID)
		if err != nil {
			return "", err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return "", errors.New("设备连接状态已改变，请重试")
		}
	}
	claims.Audience = "cwxu-proof"
	claims.ChallengeHash = challengeHash
	claims.DeviceID = client.AssignedID
	claims.NodeID = bridge.settings.GetDeployment().NodeID
	claims.IssuedAt = time.Now().Unix()
	if claims.ExpiresAt > claims.IssuedAt+30 {
		claims.ExpiresAt = claims.IssuedAt + 30
	}
	if claims.ExpiresAt <= claims.IssuedAt {
		return "", errors.New("验证请求即将过期，请重新获取")
	}
	proof, err := judgebridge.Sign(claims, bridge.secret)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO judge_presence_receipt (nonce,challenge_hash,device_id,proof,expires_at) VALUES (?,?,?,?,?)`,
		claims.Nonce, challengeHash, client.AssignedID, proof, claims.ExpiresAt); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`DELETE FROM judge_presence_receipt WHERE expires_at<?`, time.Now().Unix()-60); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if claims.Purpose == "login" {
		bridge.hub.BroadcastAdminEvent("checkin_updated", map[string]interface{}{"assigned_id": client.AssignedID})
	}
	return proof, nil
}

func attendanceContentHash(claims judgebridge.Claims) string {
	stable, _ := json.Marshal(struct {
		CID                      int
		Number, Name, Kind, Node string
		Device                   int
		Login                    int64
	}{claims.CID, claims.StudentNumber, claims.StudentName, claims.AccessKind, claims.NodeID, claims.DeviceID, claims.LoggedInAt})
	hash := sha256.Sum256(stable)
	return hex.EncodeToString(hash[:])
}

// Attendance accepts server-signed login events, including remote participants
// that do not have an assigned local device. It never invokes checkout.
func (bridge *JudgeBridge) Attendance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if len(bridge.secret) < 32 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"success": false, "message": "OJ 集成尚未配置"})
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12288)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "message": "请求格式无效"})
		return
	}
	claims, err := judgebridge.Verify(body.Token, bridge.secret, "cwxu-attendance", time.Now())
	if err != nil || !validJudgeIdentity(claims) || !judgeEventID.MatchString(claims.EventID) ||
		claims.LoggedInAt <= 0 || claims.LoggedInAt > time.Now().Unix()+5 ||
		(claims.AccessKind != "local" && claims.AccessKind != "remote" && claims.AccessKind != "exempt") ||
		(claims.AccessKind == "local" && (claims.DeviceID <= 0 || claims.NodeID == "")) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"success": false, "message": "签到事件签名或内容无效"})
		return
	}
	contentHash := attendanceContentHash(claims)
	_, err = bridge.db.Exec(`INSERT OR IGNORE INTO judge_attendance (event_id,contest_id,student_number,student_name,access_kind,node_id,device_id,logged_in_at,received_at,content_hash) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		claims.EventID, claims.CID, claims.StudentNumber, claims.StudentName, claims.AccessKind, claims.NodeID, claims.DeviceID, claims.LoggedInAt, time.Now().Unix(), contentHash)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "message": "无法保存签到事件"})
		return
	}
	var storedHash string
	if err := bridge.db.QueryRow(`SELECT content_hash FROM judge_attendance WHERE event_id=?`, claims.EventID).Scan(&storedHash); err != nil || storedHash != contentHash {
		writeJSON(w, http.StatusConflict, map[string]interface{}{"success": false, "message": "签到事件编号冲突"})
		return
	}
	bridge.hub.BroadcastAdminEvent("judge_attendance_updated", map[string]interface{}{"contest_id": claims.CID})
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (bridge *JudgeBridge) ListAttendance(w http.ResponseWriter, r *http.Request) {
	rows, err := bridge.db.Query(`SELECT event_id,contest_id,student_number,student_name,access_kind,node_id,device_id,logged_in_at FROM judge_attendance ORDER BY logged_in_at DESC LIMIT 500`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法读取比赛签到记录"})
		return
	}
	defer rows.Close()
	result := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, number, name, kind, node string
		var cid, device int
		var logged int64
		if err := rows.Scan(&id, &cid, &number, &name, &kind, &node, &device, &logged); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法读取比赛签到记录"})
			return
		}
		result = append(result, map[string]interface{}{"event_id": id, "contest_id": cid, "student_number": number,
			"student_name": name, "access_kind": kind, "node_id": node, "device_id": device, "logged_in_at": logged})
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法读取比赛签到记录"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
