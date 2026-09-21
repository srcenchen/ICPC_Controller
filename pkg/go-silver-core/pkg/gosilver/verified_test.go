package gosilver

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestVerifiedTransferRejectsCorruptResumeThenRecovers(test *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	root := test.TempDir()
	source := filepath.Join(root, "payload.bin")
	content := bytes.Repeat([]byte("verified content"), 1000)
	if err := os.WriteFile(source, content, 0600); err != nil {
		test.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	server := NewServer(address, source)
	if err := server.Start(); err != nil {
		test.Fatal(err)
	}
	defer server.Stop()
	destination := filepath.Join(root, "downloads")
	if err := os.MkdirAll(destination, 0755); err != nil {
		test.Fatal(err)
	}
	partial := filepath.Join(destination, "payload.bin."+digest+".part")
	if err := os.WriteFile(partial, make([]byte, len(content)), 0600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(partial+".icpc-chunks", []byte("[0]"), 0600); err != nil {
		test.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		client := NewClient(address, destination)
		client.SetExpectedFile("payload.bin", digest)
		progress, err := client.StartDownload()
		if err != nil {
			test.Fatal(err)
		}
		timer := time.NewTimer(15 * time.Second)
		finished := false
		var last ProgressInfo
		for !finished {
			select {
			case update, ok := <-progress:
				if !ok {
					finished = true
				} else {
					last = update
				}
			case <-timer.C:
				client.CancelDownload()
				test.Fatal("transfer timed out")
			}
		}
		timer.Stop()
		if attempt == 0 && last.Status != "failed" {
			test.Fatalf("corrupted file accepted: %s", last.Status)
		}
		if attempt == 1 && last.Status != "completed" {
			test.Fatalf("recovery failed: %s %v", last.Status, last.Error)
		}
	}
	actual, err := os.ReadFile(filepath.Join(destination, "payload.bin"))
	if err != nil || !bytes.Equal(actual, content) {
		test.Fatalf("final contents: %v", err)
	}
}

func TestConcurrentVerifiedPeers(test *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	root := test.TempDir()
	source := filepath.Join(root, "multi.bin")
	content := bytes.Repeat([]byte("parallel chunk payload"), 600000)
	if err := os.WriteFile(source, content, 0600); err != nil {
		test.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	server := NewServer(address, source)
	if err := server.Start(); err != nil {
		test.Fatal(err)
	}
	defer server.Stop()
	var workers sync.WaitGroup
	for index := 0; index < 3; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			destination := filepath.Join(root, fmt.Sprint(index))
			if err := os.MkdirAll(destination, 0755); err != nil {
				test.Error(err)
				return
			}
			client := NewClient(address, destination)
			client.SetExpectedFile("multi.bin", digest)
			progress, err := client.StartDownload()
			if err != nil {
				test.Error(err)
				return
			}
			timer := time.NewTimer(30 * time.Second)
			defer timer.Stop()
			var last ProgressInfo
			for {
				select {
				case update, ok := <-progress:
					if !ok {
						if last.Status != "completed" {
							test.Errorf("peer %d: %s %v", index, last.Status, last.Error)
						}
						return
					}
					last = update
				case <-timer.C:
					client.CancelDownload()
					test.Errorf("peer %d timed out", index)
					return
				}
			}
		}(index)
	}
	workers.Wait()
}
