package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"go-silver-core/internal/chunk"
	_const "go-silver-core/internal/const"
	"go-silver-core/internal/gsp"
	"go-silver-core/internal/gsp_sdk/model"
	"hash/crc32"
	"log"
	"net"
	"strconv"
	"time"
)

// GetFileStatus 获取文件状态请求
func (g *GspSdk) GetFileStatus() (r model.GetFileStatusResp, err error) {
	conn, err := g.connPool.GetConn(g.srvAddr)
	if err != nil {
		return
	}
	ok := false
	defer func() {
		if ok {
			g.connPool.PutConn(g.srvAddr, conn)
		} else {
			g.connPool.DiscardConn(conn)
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	req := model.BaseJson{Operate: "getFileStatus"}
	reqJson, _ := json.Marshal(req)
	if err = g.codec.EncodeTo(conn, gsp.TypeJSON, reqJson); err != nil {
		return
	}
	// 接收数据信息
	buf := g.memPool.Get(_const.ChunkSize)
	defer g.memPool.Put(buf)
	resp, decErr := g.codec.Decode(conn, *buf)
	if decErr != nil || resp == nil {
		err = fmt.Errorf("接收文件状态失败: %v", decErr)
		return
	}
	if err = json.Unmarshal(resp.Payload, &r); err != nil {
		return
	}
	ok = true
	return
}

// GetChunk 获取文件块
func (g *GspSdk) GetChunk(addr string, i int64, ck *chunk.FileChunk) (r []byte, checksum uint32, err error) {
	conn, err := g.connPool.GetConn(addr)
	if err != nil {
		return r, 0, err
	}
	ok := false
	defer func() {
		if ok {
			g.connPool.PutConn(addr, conn)
		} else {
			g.connPool.DiscardConn(conn)
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	reqG := model.GetChunkReq{Index: i, Operate: "getChunk"}
	reqJson, _ := json.Marshal(reqG)
	if err = g.codec.EncodeTo(conn, gsp.TypeJSON, reqJson); err != nil {
		return
	}
	buf := g.memPool.Get(_const.ChunkSize)
	defer g.memPool.Put(buf)
	resp, err := g.codec.Decode(conn, *buf)
	if err != nil || resp == nil {
		return nil, 0, fmt.Errorf("接收块信息失败: %v", err)
	}
	var chunkInfo model.GetChunkResp
	if err := json.Unmarshal(resp.Payload, &chunkInfo); err != nil {
		return nil, 0, fmt.Errorf("解析块信息失败: %v", err)
	}
	if chunkInfo.Index != i || !chunkInfo.Status {
		return nil, 0, errors.New("peer returned unavailable or incorrect chunk")
	}
	buf2 := g.memPool.Get(_const.ChunkSize)
	defer g.memPool.Put(buf2)
	resp, err = g.codec.Decode(conn, *buf2)
	if err != nil || resp == nil {
		return nil, 0, fmt.Errorf("missing chunk payload: %v", err)
	}
	r = resp.Payload
	curChecksum := crc32.ChecksumIEEE(resp.Payload)
	if curChecksum != chunkInfo.CheckSum {
		return r, 0, errors.New("接收块失败，Checksum校验失败")
	}
	checksum = curChecksum
	if err = ck.Save(i, resp.Payload); err != nil {
		return nil, 0, err
	}
	ok = true
	return
}

// ReportChunk 告知服务端，我是uuid 我已经拥有 第 i 块
func (g *GspSdk) ReportChunk(uuid string, i int64) error {
	conn, err := g.connPool.GetConn(g.srvAddr)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if ok {
			g.connPool.PutConn(g.srvAddr, conn)
		} else {
			g.connPool.DiscardConn(conn)
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	reqG := model.ReportChunkReq{Index: i, Operate: "reportChunk", UUID: uuid}
	reqJson, _ := json.Marshal(reqG)
	if err = g.codec.EncodeTo(conn, gsp.TypeJSON, reqJson); err != nil {
		return err
	}
	ok = true
	return nil
}

// WantChunk 向服务端请求第i块
// 服务端处理后将会返回一个地址
func (g *GspSdk) WantChunk(i int64) (*model.WantChunkResp, error) {
	conn, err := g.connPool.GetConn(g.srvAddr)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if ok {
			g.connPool.PutConn(g.srvAddr, conn)
		} else {
			g.connPool.DiscardConn(conn)
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	reqG := model.WantChunkReq{Index: i, Operate: "wantChunk"}
	reqJson, _ := json.Marshal(reqG)
	if err = g.codec.EncodeTo(conn, gsp.TypeJSON, reqJson); err != nil {
		return nil, err
	}
	buf := g.memPool.Get(_const.ChunkSize)
	defer g.memPool.Put(buf)
	resp, err := g.codec.Decode(conn, *buf)
	if err != nil {
		return nil, err
	}
	if resp.Type != gsp.TypeJSON {
		return nil, errors.New("与预期返回类型不符")
	}
	var respJ model.WantChunkResp
	err = json.Unmarshal(resp.Payload, &respJ)
	if err != nil {
		return nil, errors.New("JSON 解析失败")
	}
	ok = true
	return &respJ, nil
}

// PeerReg Peer 节点注册 (dedicated dial, not from pool — holds conn for session lifetime)
func (g *GspSdk) PeerReg(peerPort int, uuid string) error {
	controlConn, err := net.DialTimeout("tcp", g.srvAddr, 10*time.Second)
	if err != nil {
		return err
	}
	g.mu.Lock()
	if g.control != nil {
		g.control.Close()
	}
	g.control = controlConn
	g.mu.Unlock()
	codec := gsp.Codec{}
	jsonReq, _ := json.Marshal(model.PeerRegReq{
		Operate: "peerReg",
		Port:    strconv.Itoa(peerPort),
		UUID:    uuid,
	})
	if err := codec.EncodeTo(controlConn, gsp.TypeJSON, jsonReq); err != nil {
		controlConn.Close()
		return err
	}
	// 控制流保活
	go func() {
		defer controlConn.Close()
		buf := [1]byte{}
		_, _ = codec.Decode(controlConn, buf[:])
		log.Println("[client] 与分发服务端控制连接断开")
	}()
	return nil
}

// ReportPeer 向服务端发送Peer信息，包括提供下载的对端UUID和本次状态
func (g *GspSdk) ReportPeer(uuid string, providerUuid string, speed int64, status string) error {
	conn, err := g.connPool.GetConn(g.srvAddr)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if ok {
			g.connPool.PutConn(g.srvAddr, conn)
		} else {
			g.connPool.DiscardConn(conn)
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	reqG := model.PeerReportReq{
		Operate:      "reportPeer",
		UUID:         uuid,
		ProviderUUID: providerUuid,
		Status:       status,
		Speed:        speed,
	}
	reqJson, _ := json.Marshal(reqG)
	if err = g.codec.EncodeTo(conn, gsp.TypeJSON, reqJson); err != nil {
		return err
	}
	ok = true
	return nil
}
