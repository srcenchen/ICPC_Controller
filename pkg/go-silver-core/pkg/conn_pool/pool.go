package conn_pool

import (
	"net"
	"sync"
	"time"
)

// ConnPool 连接池
type ConnPool struct {
	mu          sync.Mutex
	conn        map[string]chan net.Conn
	maxPerConn  int
	dialTimeout time.Duration
}

func NewConnPool(maxPerConn int) *ConnPool {
	return &ConnPool{
		conn:        make(map[string]chan net.Conn),
		maxPerConn:  maxPerConn,
		dialTimeout: 10 * time.Second,
	}
}

// GetConn 借出连接
func (cp *ConnPool) GetConn(addr string) (net.Conn, error) {
	cp.mu.Lock()
	ch, ok := cp.conn[addr]
	if !ok {
		ch = make(chan net.Conn, cp.maxPerConn)
		cp.conn[addr] = ch
	}
	cp.mu.Unlock()

	select {
	case conn := <-ch:
		return conn, nil
	default:
		return net.DialTimeout("tcp", addr, cp.dialTimeout)
	}
}

// PutConn 归还连接
func (cp *ConnPool) PutConn(addr string, conn net.Conn) {
	if conn == nil {
		return
	}
	cp.mu.Lock()
	ch, ok := cp.conn[addr]
	if !ok {
		cp.mu.Unlock()
		conn.Close()
		return
	}
	cp.mu.Unlock()
	select {
	case ch <- conn:
	default:
		conn.Close()
	}
}

// DiscardConn closes a bad connection instead of returning it to the pool.
func (cp *ConnPool) DiscardConn(conn net.Conn) {
	if conn != nil {
		conn.Close()
	}
}
