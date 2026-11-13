package gsp

import (
	"bytes"
	"net"
	"testing"

	gsp "go-silver-core/internal/gsp"
)

// tcpPair returns both ends of a loopback TCP connection. net.Pipe is not
// used because a zero-length write on it blocks until the peer reads.
func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := lis.Accept()
		accepted <- conn
	}()
	client, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

// TestCodecRoundTrip encodes packets on one end of a connection and decodes
// them on the other.
func TestCodecRoundTrip(t *testing.T) {
	client, server := tcpPair(t)

	codec := gsp.Codec{}
	packets := []struct {
		typ     uint8
		payload []byte
	}{
		{gsp.TypeJSON, []byte(`{"chunks":3,"size":12345}`)},
		{gsp.TypeFileChunk, bytes.Repeat([]byte{0xAB}, 64<<10)},
		{gsp.TypeJSON, []byte{}},
	}

	errs := make(chan error, 1)
	go func() {
		for _, p := range packets {
			if err := codec.EncodeTo(client, p.typ, p.payload); err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()

	buf := make([]byte, 128<<10)
	for i, want := range packets {
		got, err := codec.Decode(server, buf)
		if err != nil {
			t.Fatalf("packet %d: decode: %v", i, err)
		}
		if got.Type != want.typ || int(got.Length) != len(want.payload) || !bytes.Equal(got.Payload, want.payload) {
			t.Fatalf("packet %d: got type=%d len=%d, want type=%d len=%d", i, got.Type, got.Length, want.typ, len(want.payload))
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
}

func TestCodecRejectsOversizedPayload(t *testing.T) {
	client, server := tcpPair(t)

	codec := gsp.Codec{}
	go func() { _ = codec.EncodeTo(client, gsp.TypeFileChunk, make([]byte, 1024)) }()
	if _, err := codec.Decode(server, make([]byte, 16)); err == nil {
		t.Fatal("decoding a payload larger than the buffer should fail")
	}
}
