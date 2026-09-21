package conn_pool

import (
	"errors"
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
	closed      bool
	all         map[net.Conn]bool
}

func NewConnPool(maxPerConn int) *ConnPool {
	return &ConnPool{
		conn:        make(map[string]chan net.Conn),
		maxPerConn:  maxPerConn,
		dialTimeout: 10 * time.Second,
		all:         make(map[net.Conn]bool),
	}
}

// GetConn 借出连接
func (cp *ConnPool) GetConn(addr string) (net.Conn, error) {
	cp.mu.Lock()
	if cp.closed {
		cp.mu.Unlock()
		return nil, errors.New("connection pool closed")
	}
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
		conn, err := net.DialTimeout("tcp", addr, cp.dialTimeout)
		if err != nil {
			return nil, err
		}
		cp.mu.Lock()
		defer cp.mu.Unlock()
		if cp.closed {
			conn.Close()
			return nil, errors.New("connection pool closed")
		}
		cp.all[conn] = true
		return conn, nil
	}
}

// PutConn 归还连接
func (cp *ConnPool) PutConn(addr string, conn net.Conn) {
	if conn == nil {
		return
	}
	cp.mu.Lock()
	ch, ok := cp.conn[addr]
	if !ok || cp.closed {
		cp.mu.Unlock()
		conn.Close()
		return
	}
	defer cp.mu.Unlock()
	select {
	case ch <- conn:
	default:
		conn.Close()
		delete(cp.all, conn)
	}
}

// DiscardConn closes a bad connection instead of returning it to the pool.
func (cp *ConnPool) DiscardConn(conn net.Conn) {
	if conn != nil {
		conn.Close()
		cp.mu.Lock()
		delete(cp.all, conn)
		cp.mu.Unlock()
	}
}

func (cp *ConnPool) Close() {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.closed = true
	for conn := range cp.all {
		conn.Close()
		delete(cp.all, conn)
	}
}
