package chunk

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"go-silver-core/internal/chunk"
	_const "go-silver-core/internal/const"
	"go-silver-core/pkg/mempool"
)

// TestFileChunkRoundTrip splits a file that does not end on a chunk boundary,
// copies it chunk by chunk into a pre-sized file, and checks content and CRCs.
func TestFileChunkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	size := int64(_const.ChunkSize*2 + 12345)
	content := make([]byte, size)
	if _, err := rand.Read(content); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(srcPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	// A tiny pool size makes Get fall back to fresh allocations instead of
	// preallocating 50 full chunks.
	pool := mempool.NewMemPool(1)
	in := chunk.NewFileChunk(src, pool)
	if got, want := in.GetChunkNum(), int64(3); got != want {
		t.Fatalf("GetChunkNum = %d, want %d", got, want)
	}

	dst, err := os.Create(filepath.Join(dir, "dst.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.Truncate(size); err != nil {
		t.Fatal(err)
	}
	out := chunk.NewFileChunk(dst, pool)

	buf := make([]byte, in.GetChunkSize())
	for i := int64(0); i < in.GetChunkNum(); i++ {
		n, err := in.ReadChunk(i, buf)
		if err != nil {
			t.Fatalf("ReadChunk(%d): %v", i, err)
		}
		if err := out.Save(i, buf[:n]); err != nil {
			t.Fatalf("Save(%d): %v", i, err)
		}
	}
	for i := int64(0); i < in.GetChunkNum(); i++ {
		a, err := in.CheckSum(i)
		if err != nil {
			t.Fatal(err)
		}
		b, err := out.CheckSum(i)
		if err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Fatalf("chunk %d crc mismatch: %x != %x", i, a, b)
		}
	}
	got, err := os.ReadFile(dst.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("copied file differs from source")
	}
}

func TestFileChunkRejectsBadInput(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "small")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	c := chunk.NewFileChunk(f, mempool.NewMemPool(1))
	buf := make([]byte, c.GetChunkSize())
	if _, err := c.ReadChunk(1, buf); err == nil {
		t.Error("ReadChunk past the end should fail")
	}
	if err := c.Save(0, []byte("hi")); err == nil {
		t.Error("Save with the wrong length should fail")
	}
}
