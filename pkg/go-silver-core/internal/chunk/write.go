package chunk

import (
	"fmt"
	"io"
)

func (f *FileChunk) Save(index int64, data []byte) error {
	if index < 0 || index >= f.GetChunkNum() {
		return fmt.Errorf("invalid chunk index")
	}
	expected := f.chunkSize
	if remaining := f.FileStat.Size() - index*f.chunkSize; remaining < expected {
		expected = remaining
	}
	if int64(len(data)) != expected {
		return fmt.Errorf("invalid chunk length")
	}
	f.ioPermit <- struct{}{}
	defer func() { <-f.ioPermit }()
	written, err := f.file.WriteAt(data, index*f.chunkSize)
	if err == nil && written != len(data) {
		return io.ErrShortWrite
	}
	return err
}
