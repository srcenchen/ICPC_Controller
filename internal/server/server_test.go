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
