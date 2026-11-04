package server

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/service"
)

func TestServerRegistersFederatedRoutes(test *testing.T) {
	db, err := data.NewDB(filepath.Join(test.TempDir(), "test.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer db.Close()
	repo := data.NewDeviceRepo(db)
	hub := biz.NewHub(repo)
	settings := service.NewServerSettings("test", nil)
	manager := service.NewDistributionManager(hub, filepath.Join(test.TempDir(), "uploads"))
	federation := service.NewFederation(db, settings, repo, manager, hub)
	server := New(Config{Port: "0", Federation: federation})
	defer server.stopBackground()
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/cluster/status", nil))
	if response.Code != 200 {
		test.Fatalf("cluster route: %d", response.Code)
	}
}

func TestJudgeAttendanceKeepsAdminReadsProtected(test *testing.T) {
	db, err := data.NewDB(filepath.Join(test.TempDir(), "judge.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer db.Close()
	repo := data.NewDeviceRepo(db)
	hub := biz.NewHub(repo)
	settings := service.NewServerSettings("test", nil)
	bridge, err := service.NewJudgeBridge(db, repo, hub, settings, "test-secret-with-at-least-32-bytes", []string{"http://127.0.0.1:8080"})
	if err != nil {
		test.Fatal(err)
	}
	server := New(Config{Port: "0", JudgeBridge: bridge, AuthH: service.NewAuthHandler(settings)})
	defer server.stopBackground()
	for _, entry := range []struct {
		method string
		want   int
	}{{"GET", 401}, {"POST", 400}, {"DELETE", 401}} {
		response := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(entry.method, "/api/judge/attendance", nil))
		if response.Code != entry.want {
			test.Fatalf("%s attendance: %d want %d", entry.method, response.Code, entry.want)
		}
	}
}
