package client

import (
	"go-silver-core/internal/gsp"
	"go-silver-core/pkg/conn_pool"
	"go-silver-core/pkg/mempool"
	"net"
	"sync"
)

// GspSdk 大多数的功能是给 receiver 端调用的
type GspSdk struct {
	srvAddr  string
	codec    gsp.Codec
	connPool *conn_pool.ConnPool
	memPool  *mempool.MemPool
	mu       sync.Mutex
	control  net.Conn
}

func (g *GspSdk) Close() {
	g.connPool.Close()
	g.mu.Lock()
	if g.control != nil {
		g.control.Close()
	}
	g.mu.Unlock()
}

func NewGspSdk(srvAddr string, memPool *mempool.MemPool) GspSdk {
	connPool := conn_pool.NewConnPool(10)
	return GspSdk{connPool: connPool, srvAddr: srvAddr, codec: gsp.Codec{}, memPool: memPool}
}
