package server

import (
	"encoding/json"
	"go-silver-core/internal/gsp"
	"go-silver-core/internal/gsp_sdk/model"
	"log/slog"
	"net"
)

// TODO 测试用队列
type queue2 struct {
	s *Session
}

func isReachableSubnet(reqIP, peerIP string) bool {
	netIP1 := net.ParseIP(reqIP)
	netIP2 := net.ParseIP(peerIP)
	if netIP1 == nil || netIP2 == nil {
		return false
	}
	ip1v4 := netIP1.To4()
	ip2v4 := netIP2.To4()
	if ip1v4 != nil && ip2v4 != nil {
		// Compare first 3 bytes (24-bit subnet mask / Class C)
		return ip1v4[0] == ip2v4[0] && ip1v4[1] == ip2v4[1] && ip1v4[2] == ip2v4[2]
	}
	// For IPv6, compare first 48 bits
	ip1v6 := netIP1.To16()
	ip2v6 := netIP2.To16()
	if ip1v6 != nil && ip2v6 != nil {
		return ip1v6[0] == ip2v6[0] && ip1v6[1] == ip2v6[1] && ip1v6[2] == ip2v6[2] &&
			ip1v6[3] == ip2v6[3] && ip1v6[4] == ip2v6[4] && ip1v6[5] == ip2v6[5]
	}
	return false
}

func (q *queue2) Want(i int64, conn net.Conn) {
	c := gsp.Codec{}

	// 获取请求客户端的 IP
	reqIP := conn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(reqIP); err == nil {
		reqIP = host
	}

	// 锁定 Session 读取数据，保证线程安全
	q.s.mu.RLock()

	var bestUUID string
	var maxScore float64 = -1.0
	const baseWeight = 10.0
	// 1. 遍历所有拥有此分块的 Peer
	owners := q.s.ChunkOwners[i]
	if len(owners) == 0 {
		// 如果没人有，默认指向自己（或者报错）
		bestUUID = q.s.UUID
	} else {
		for uid := range owners {
			if uid == q.s.UUID {
				continue
			}

			peer, ok := q.s.Peers[uid]
			if !ok {
				continue
			}

			// 获取候选 Peer 的 IP
			peerIP := peer.connAddr
			if host, _, err := net.SplitHostPort(peerIP); err == nil {
				peerIP = host
			}

			// 如果候选 Peer 和当前请求端不在同一个子网内，说明无法访问，过滤掉
			if !isReachableSubnet(reqIP, peerIP) {
				continue
			}

			// 核心算法：基础权重+实际速度 / (连接数+1)的平方
			// 使用平方来快速衰减高并发节点的分数
			denominator := float64(peer.connNum + 1)
			score := (baseWeight + float64(peer.maxSpeed)) / (denominator * denominator)

			if score > maxScore {
				maxScore = score
				bestUUID = uid
			}
		}
	}

	// 如果遍历完发现没有合适的第三方节点，回退到自己
	if bestUUID == "" {
		bestUUID = q.s.UUID
	}

	// 获取最终选定的 Peer 信息
	targetPeer, ok := q.s.Peers[bestUUID]
	var targetAddr string
	if !ok || bestUUID == q.s.UUID {
		bestUUID = q.s.UUID
		targetAddr = "" // Session 自身的地址
	} else {
		targetAddr = targetPeer.connAddr
	}
	q.s.mu.RUnlock() // 释放读锁

	// 3. 更新连接数计数（需要写锁）
	q.s.mu.Lock()
	if p, ok := q.s.Peers[bestUUID]; ok {
		p.connNum++
	}
	q.s.mu.Unlock()

	// 4. 编码并发送响应
	jc, _ := json.Marshal(model.WantChunkResp{
		Index:    i,
		Addr:     targetAddr,
		CheckSum: 0, // 实际应用中应计算校验和
		UUID:     bestUUID,
	})

	if err := c.EncodeTo(conn, gsp.TypeJSON, jc); err != nil {
		slog.Error("发送 WantChunkResp 失败", "error", err)
	}
}
