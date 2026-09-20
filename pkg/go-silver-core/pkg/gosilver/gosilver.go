package gosilver

import (
	"context"
	"encoding/json"
	"fmt"
	_const "go-silver-core/internal/const"
	"go-silver-core/internal/gsp_sdk/client"
	"go-silver-core/internal/gsp_sdk/model"
	"go-silver-core/internal/gsp_sdk/server"
	"go-silver-core/pkg/mempool"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// ProgressInfo 包含当前的下载进度状态
type ProgressInfo struct {
	TotalChunks int64   // 总分块数
	Downloaded  int64   // 已下载的分块数
	Percentage  float64 // 下载百分比 (0.0 到 100.0)
	SpeedMbps   int64   // 当前下载速度 (Mbps)
	Status      string  // 状态: "idle", "downloading", "completed", "failed", "cancelled"
	Error       error   // 错误信息 (如果失败)
}

// Server 用于管理文件分发主服务端 (Sender) 的启动和停止
type Server struct {
	addr     string
	filePath string
	mp       *mempool.MemPool
	session  *server.Session
	file     *os.File
}

// NewServer 创建一个新的服务端实例
// addr: 服务端监听地址，例如 ":48080"
// filePath: 要分发的文件路径
func NewServer(addr string, filePath string) *Server {
	return &Server{
		addr:     addr,
		filePath: filePath,
	}
}

// SetPeerSelectMode configures P2P peer filtering: "subnet" (default) or "all".
func SetPeerSelectMode(mode string) {
	server.SetPeerSelectMode(mode)
}

// Start 启动服务端并开始监听和分块
func (s *Server) Start() error {
	s.mp = mempool.NewMemPool(_const.ChunkSize)
	s.session = server.NewGspSession(s.addr, s.mp)

	if err := s.session.Start(); err != nil {
		return err
	}

	f, err := os.Open(s.filePath)
	if err != nil {
		s.session.Stop()
		return err
	}
	s.file = f

	if err := s.session.BeSendMain(f); err != nil {
		f.Close()
		s.session.Stop()
		return err
	}

	return nil
}

// Stop 停止服务端监听并关闭文件
func (s *Server) Stop() {
	if s.session != nil {
		s.session.Stop()
	}
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
}

// Client 用于管理文件接收客户端 (Receiver) 的下载和 P2P 上报
type Client struct {
	senderAddr string
	saveDir    string
	peerPort   int
	mp         *mempool.MemPool
	session    *server.Session
	file       *os.File

	mu         sync.Mutex
	status     ProgressInfo
	progressCh chan ProgressInfo
	chClosed   bool
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewClient 创建一个新的客户端实例
// senderAddr: 主发送端/服务端的地址，例如 "192.168.1.10:48080"
// saveDir: 文件保存目录，如果为空则保存在当前目录
func NewClient(senderAddr string, saveDir string) *Client {
	return &Client{
		senderAddr: senderAddr,
		saveDir:    saveDir,
		progressCh: make(chan ProgressInfo, 100),
		status: ProgressInfo{
			Status: "idle",
		},
	}
}

// StartDownload 启动非阻塞的文件下载过程，返回一个用于接收进度反馈的通道
func (c *Client) StartDownload() (<-chan ProgressInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status.Status == "downloading" {
		return nil, fmt.Errorf("download already in progress")
	}

	// Fresh progress channel per download so prior close does not break reuse.
	c.progressCh = make(chan ProgressInfo, 100)
	c.chClosed = false
	c.status = ProgressInfo{
		Status: "downloading",
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	c.wg.Add(1)
	go c.runDownload(ctx)

	return c.progressCh, nil
}

// CancelDownload 取消当前的下载，并同步阻塞直到协程完全退出并释放所有资源
func (c *Client) CancelDownload() {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

// GetStatus 获取当前的下载状态副本
func (c *Client) GetStatus() ProgressInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Client) updateProgress(info ProgressInfo) {
	c.mu.Lock()
	ch := c.progressCh
	closed := c.chClosed
	c.mu.Unlock()
	if closed || ch == nil {
		return
	}
	select {
	case ch <- info:
	default:
		// 如果通道满了，移除旧消息放入新消息，防止阻塞下载过程
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- info:
		default:
		}
	}
}

func (c *Client) closeProgress() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.chClosed && c.progressCh != nil {
		c.chClosed = true
		close(c.progressCh)
	}
}

func (c *Client) finishWithError(err error, contextMsg string) {
	c.mu.Lock()
	c.status.Status = "failed"
	c.status.Error = fmt.Errorf("%s: %w", contextMsg, err)
	info := c.status
	c.mu.Unlock()
	c.updateProgress(info)
}

func (c *Client) finishCancelled() {
	c.mu.Lock()
	c.status.Status = "cancelled"
	c.status.Error = context.Canceled
	info := c.status
	c.mu.Unlock()
	c.updateProgress(info)
}

func (c *Client) runDownload(ctx context.Context) {
	defer c.wg.Done()
	defer c.closeProgress()

	c.mp = mempool.NewMemPool(_const.ChunkSize)

	// 随机分配子节点端口，监听失败则重试几次
	var startErr error
	for attempt := 0; attempt < 20; attempt++ {
		c.peerPort = rand.IntN(999) + 3000
		c.session = server.NewGspSession(":"+strconv.Itoa(c.peerPort), c.mp)
		startErr = c.session.Start()
		if startErr == nil {
			break
		}
	}
	if startErr != nil {
		c.finishWithError(startErr, "failed to start peer server")
		return
	}
	defer c.session.Stop()

	gspC := client.NewGspSdk(c.senderAddr, c.mp)
	var status model.GetFileStatusResp
	for {
		var err error
		status, err = gspC.GetFileStatus()
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			c.finishCancelled()
			return
		default:
		}
		c.mu.Lock()
		c.status.Error = fmt.Errorf("正在重连服务端 (每5秒重试): %w", err)
		info := c.status
		c.mu.Unlock()
		c.updateProgress(info)
		time.Sleep(5 * time.Second)
	}

	c.mu.Lock()
	c.status.TotalChunks = status.ChunkNum
	c.mu.Unlock()

	fileName := status.FileName
	if c.saveDir != "" {
		fileName = filepath.Join(c.saveDir, fileName)
	}
	metaPath := fileName + ".icpc-chunks"

	// Resume: open existing file if size matches; otherwise create fresh.
	var f *os.File
	var err error
	doneSet := loadChunkMeta(metaPath)
	if fi, statErr := os.Stat(fileName); statErr == nil && fi.Size() == status.FileSize {
		f, err = os.OpenFile(fileName, os.O_RDWR, 0644)
	} else {
		doneSet = make(map[int64]bool)
		_ = os.Remove(metaPath)
		f, err = os.Create(fileName)
		if err == nil {
			err = f.Truncate(status.FileSize)
		}
	}
	if err != nil {
		c.finishWithError(err, "failed to open local file")
		return
	}
	c.file = f
	defer func() {
		if c.file != nil {
			_ = c.file.Close()
			c.file = nil
		}
		c.mu.Lock()
		statusStr := c.status.Status
		c.mu.Unlock()
		if statusStr == "completed" {
			_ = os.Remove(metaPath)
		}
		// Keep partial file + meta for resume on failed/cancelled.
	}()

	c.session.BeSendSub(f)
	ck := c.session.GetChunk()

	// 注册 Peer
	for {
		err := gspC.PeerReg(c.peerPort, c.session.UUID)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			c.finishCancelled()
			return
		default:
		}
		c.mu.Lock()
		c.status.Error = fmt.Errorf("注册对端失败，正在重试 (每5秒重试): %w", err)
		info := c.status
		c.mu.Unlock()
		c.updateProgress(info)
		time.Sleep(5 * time.Second)
	}

	// 准备分块索引（跳过已完成，支持断点续传）
	var indices []int64
	var downloadedCount int64
	for i := int64(0); i < status.ChunkNum; i++ {
		if doneSet[i] {
			// Re-register owned chunk for P2P serving (checksum 0 is fine for ownership).
			c.session.AddChunk(i, 0)
			_ = gspC.ReportChunk(c.session.UUID, i)
			downloadedCount++
			continue
		}
		indices = append(indices, i)
	}
	if status.ChunkNum > 0 {
		c.mu.Lock()
		c.status.Downloaded = downloadedCount
		c.status.Percentage = float64(downloadedCount) / float64(status.ChunkNum) * 100
		info := c.status
		c.mu.Unlock()
		c.updateProgress(info)
	}

	// 打乱顺序，优化局域网 P2P
	rand.Shuffle(len(indices), func(i, j int) {
		indices[i], indices[j] = indices[j], indices[i]
	})

	for len(indices) > 0 {
		select {
		case <-ctx.Done():
			saveChunkMeta(metaPath, doneSet)
			c.finishCancelled()
			return
		default:
		}

		var failedList []int64
		var mu sync.Mutex
		var wg sync.WaitGroup
		limit := make(chan struct{}, 5) // 控制并发数

		for _, idx := range indices {
			select {
			case <-ctx.Done():
				// drain workers
			default:
			}

			wg.Add(1)
			limit <- struct{}{}

			go func(i int64) {
				defer wg.Done()
				defer func() { <-limit }()

				select {
				case <-ctx.Done():
					mu.Lock()
					failedList = append(failedList, i)
					mu.Unlock()
					return
				default:
				}

				// 询问 Tracker 分块位置
				reChunk, err := gspC.WantChunk(i)
				if err != nil {
					mu.Lock()
					failedList = append(failedList, i)
					mu.Unlock()
					return
				}

				targetAddr := reChunk.Addr
				if targetAddr == "" {
					targetAddr = c.senderAddr
				}

				// 开始下载
				tBegin := time.Now()
				_, cm, err := gspC.GetChunk(targetAddr, i, &ck)
				if err != nil {
					// 上报失败，以扣减提供端的并发连接数
					_ = gspC.ReportPeer(c.session.UUID, reChunk.UUID, 0, "failed")
					mu.Lock()
					failedList = append(failedList, i)
					mu.Unlock()
					return
				}

				duration := time.Since(tBegin).Microseconds()
				var speedMbps int64 = 0
				if duration > 0 {
					speedMbps = (_const.ChunkSize / duration) * 8
				}

				c.session.AddChunk(i, cm)
				_ = gspC.ReportChunk(c.session.UUID, i)
				_ = gspC.ReportPeer(c.session.UUID, reChunk.UUID, speedMbps, "done")

				mu.Lock()
				doneSet[i] = true
				mu.Unlock()

				c.mu.Lock()
				downloadedCount++
				c.status.Downloaded = downloadedCount
				if status.ChunkNum > 0 {
					c.status.Percentage = float64(downloadedCount) / float64(status.ChunkNum) * 100
				}
				c.status.SpeedMbps = speedMbps
				info := c.status
				c.mu.Unlock()
				c.updateProgress(info)
			}(idx)
		}
		wg.Wait()

		select {
		case <-ctx.Done():
			saveChunkMeta(metaPath, doneSet)
			c.finishCancelled()
			return
		default:
		}

		if len(failedList) > 0 {
			if len(failedList) == len(indices) {
				select {
				case <-ctx.Done():
					saveChunkMeta(metaPath, doneSet)
					c.finishCancelled()
					return
				case <-time.After(5 * time.Second):
				}
			} else {
				time.Sleep(500 * time.Millisecond)
			}
		}
		// Persist progress for resume
		saveChunkMeta(metaPath, doneSet)
		indices = failedList
	}

	c.mu.Lock()
	c.status.Status = "completed"
	c.status.Percentage = 100.0
	c.status.SpeedMbps = 0
	info := c.status
	c.mu.Unlock()
	c.updateProgress(info)
}

func loadChunkMeta(path string) map[int64]bool {
	set := make(map[int64]bool)
	data, err := os.ReadFile(path)
	if err != nil {
		return set
	}
	var ids []int64
	if json.Unmarshal(data, &ids) != nil {
		return set
	}
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func saveChunkMeta(path string, set map[int64]bool) {
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}
