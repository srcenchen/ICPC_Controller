package server

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"ICPCRemoteControl/internal/service"
)

//go:embed web/*
var webFS embed.FS

// Server is the main HTTP server.
type Server struct {
	stopBackground context.CancelFunc
	httpServer     *http.Server
	avahiCmd       *exec.Cmd
	bindIP         string
	enableAvahi    bool
}

// Config holds server configuration.
type Config struct {
	Federation    *service.Federation
	BackupH       *service.BackupHandler
	Port          string
	BindIP        string
	DBPath        string
	Avahi         bool
	DeviceH       *service.DeviceHandler
	CommandH      *service.CommandHandler
	StatsH        *service.StatsHandler
	AdminWSH      *service.AdminWSHandler
	TerminalWSH   *service.TerminalWSHandler
	SettingsH     *service.SettingsHandler
	NetworkH      *service.NetworkHandler
	CheckinH      *service.CheckinHandler
	BroadcastH    *service.BroadcastHandler
	AuthH         *service.AuthHandler
	DistributionH *service.DistributionHandler
	ScreenProxyH  *service.ScreenProxyHandler
	PowerH        *service.PowerHandler
	InstallH      *service.InstallHandler
	Snapshots     *service.SnapshotManager
}

// New creates a new Server.
func New(cfg Config) *Server {
	mux := http.NewServeMux()
	if cfg.Federation != nil {
		mux.HandleFunc("GET /api/cluster/status", cfg.Federation.Status)
		mux.HandleFunc("GET /api/cluster/rooms", cfg.Federation.Rooms)
		mux.HandleFunc("POST /api/cluster/jobs", cfg.Federation.Queue)
		mux.HandleFunc("GET /api/cluster/jobs", cfg.Federation.Jobs)
		mux.HandleFunc("POST /api/cluster/broadcast", cfg.Federation.PublishBroadcast)
		mux.HandleFunc("DELETE /api/cluster/jobs/{id}", cfg.Federation.CancelJob)
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			mux.HandleFunc(method+" /api/cluster/rooms/{room}/proxy/{path...}", cfg.Federation.Proxy)
		}
		mux.HandleFunc("GET /ws/cluster/rooms/{room}/terminal/{id}", cfg.Federation.Terminal)
	}
	if cfg.BackupH != nil {
		mux.HandleFunc("GET /api/data/backup", cfg.BackupH.Download)
		mux.HandleFunc("GET /api/data/export", cfg.BackupH.Export)
	}

	if cfg.AuthH != nil {
		mux.HandleFunc("POST /api/auth/login", cfg.AuthH.Login)
		mux.HandleFunc("POST /api/auth/logout", cfg.AuthH.Logout)
		mux.HandleFunc("POST /api/auth/password", cfg.AuthH.ChangePassword)
	}

	mux.HandleFunc("GET /api/stats", cfg.StatsH.GetStats)
	mux.HandleFunc("GET /api/devices", cfg.DeviceH.List)
	mux.HandleFunc("GET /api/devices/export", cfg.DeviceH.ExportXLSX)
	mux.HandleFunc("GET /api/devices/{id}", cfg.DeviceH.Get)
	mux.HandleFunc("GET /api/devices/{id}/events", cfg.DeviceH.Events)
	mux.HandleFunc("DELETE /api/devices/{id}", cfg.DeviceH.Delete)
	mux.HandleFunc("POST /api/devices/reset", cfg.DeviceH.Reset)
	mux.HandleFunc("POST /api/commands", cfg.CommandH.Execute)
	mux.HandleFunc("GET /api/commands", cfg.CommandH.List)
	mux.HandleFunc("GET /api/commands/{id}", cfg.CommandH.Get)
	mux.HandleFunc("POST /api/commands/{id}/cancel", cfg.CommandH.Cancel)
	mux.HandleFunc("POST /api/commands/clear", cfg.CommandH.Clear)
	mux.HandleFunc("GET /api/presets", cfg.CommandH.Presets)
	mux.HandleFunc("GET /api/settings", cfg.SettingsH.Get)
	mux.HandleFunc("POST /api/settings", cfg.SettingsH.Update)
	mux.HandleFunc("GET /api/settings/presets", cfg.SettingsH.GetPresets)
	mux.HandleFunc("PUT /api/settings/presets", cfg.SettingsH.UpdatePresets)
	mux.HandleFunc("GET /api/settings/checkin", cfg.SettingsH.GetCheckinConfig)
	mux.HandleFunc("PUT /api/settings/checkin", cfg.SettingsH.UpdateCheckinConfig)

	mux.HandleFunc("GET /api/network/rules", cfg.NetworkH.GetRules)
	mux.HandleFunc("PUT /api/network/rules", cfg.NetworkH.UpdateRules)
	mux.HandleFunc("POST /api/network/apply", cfg.NetworkH.Apply)
	mux.HandleFunc("POST /api/network/remove", cfg.NetworkH.Remove)

	mux.HandleFunc("GET /api/checkin", cfg.CheckinH.List)
	mux.HandleFunc("GET /api/checkin/export", cfg.CheckinH.ExportXLSX)
	mux.HandleFunc("GET /api/checkin/stats", cfg.CheckinH.Stats)
	mux.HandleFunc("POST /api/checkin/{id}/checkin", cfg.CheckinH.DoCheckin)
	mux.HandleFunc("POST /api/checkin/{id}/checkout", cfg.CheckinH.DoCheckout)
	mux.HandleFunc("POST /api/checkin/{id}/restore", cfg.CheckinH.DoRestoreCheckout)
	mux.HandleFunc("POST /api/checkin/{id}/reset", cfg.CheckinH.Reset)

	mux.HandleFunc("POST /api/checkin/swap", cfg.CheckinH.Swap)
	mux.HandleFunc("POST /api/checkin/reset-all", cfg.CheckinH.ResetAll)

	// Broadcast management API.
	if cfg.BroadcastH != nil {
		mux.HandleFunc("GET /api/broadcast/pages", cfg.BroadcastH.ListPages)
		mux.HandleFunc("POST /api/broadcast/pages", cfg.BroadcastH.CreatePage)
		mux.HandleFunc("PUT /api/broadcast/pages/{id}", cfg.BroadcastH.UpdatePage)
		mux.HandleFunc("DELETE /api/broadcast/pages/{id}", cfg.BroadcastH.DeletePage)
		mux.HandleFunc("POST /api/broadcast/pages/{id}/duplicate", cfg.BroadcastH.DuplicatePage)
		mux.HandleFunc("PUT /api/broadcast/pages/reorder", cfg.BroadcastH.ReorderPages)
		mux.HandleFunc("GET /api/broadcast/items", cfg.BroadcastH.ListItems)
		mux.HandleFunc("POST /api/broadcast/items", cfg.BroadcastH.CreateItem)
		mux.HandleFunc("PUT /api/broadcast/items/{id}", cfg.BroadcastH.UpdateItem)
		mux.HandleFunc("PATCH /api/broadcast/items/{id}/position", cfg.BroadcastH.UpdateItemPosition)
		mux.HandleFunc("DELETE /api/broadcast/items/{id}", cfg.BroadcastH.DeleteItem)
		mux.HandleFunc("GET /api/broadcast/fonts", cfg.BroadcastH.ListFonts)
		mux.HandleFunc("POST /api/broadcast/fonts", cfg.BroadcastH.UploadFont)
		mux.HandleFunc("DELETE /api/broadcast/fonts/{id}", cfg.BroadcastH.DeleteFont)
		mux.HandleFunc("POST /api/broadcast/images/upload", cfg.BroadcastH.UploadImage)
		mux.HandleFunc("GET /api/broadcast/config", cfg.BroadcastH.GetConfig)
		mux.HandleFunc("PUT /api/broadcast/config", cfg.BroadcastH.UpdateConfig)
		mux.HandleFunc("GET /api/broadcast/config/countdown", cfg.BroadcastH.GetCountdown)
		mux.HandleFunc("GET /broadcast/fonts/{filename}", cfg.BroadcastH.ServeFont)
		mux.HandleFunc("GET /broadcast/images/{filename}", cfg.BroadcastH.ServeImage)
	}

	if cfg.DistributionH != nil {
		mux.HandleFunc("GET /api/distribution/files", cfg.DistributionH.ListFiles)
		mux.HandleFunc("POST /api/distribution/upload", cfg.DistributionH.UploadFile)
		mux.HandleFunc("POST /api/distribution/delete", cfg.DistributionH.DeleteFiles)
		mux.HandleFunc("POST /api/distribution/clear", cfg.DistributionH.ClearFiles)
		mux.HandleFunc("GET /api/distribution/status", cfg.DistributionH.GetStatus)
		mux.HandleFunc("POST /api/distribution/start", cfg.DistributionH.StartTask)
		mux.HandleFunc("POST /api/distribution/stop", cfg.DistributionH.StopTask)
		mux.HandleFunc("POST /api/distribution/retry", cfg.DistributionH.RetryDevice)
		mux.HandleFunc("POST /api/distribution/precheck", cfg.DistributionH.Precheck)
		mux.HandleFunc("POST /api/distribution/reset", cfg.DistributionH.ResetTask)
	}

	if cfg.ScreenProxyH != nil {
		mux.HandleFunc("GET /api/devices/{id}/screen", cfg.ScreenProxyH.Proxy)
	}

	if cfg.PowerH != nil {
		mux.HandleFunc("POST /api/power/wol", cfg.PowerH.Wake)
		mux.HandleFunc("GET /api/power/schedules", cfg.PowerH.Schedules)
		mux.HandleFunc("POST /api/power/schedules", cfg.PowerH.Schedules)
		mux.HandleFunc("DELETE /api/power/schedules/{id}", cfg.PowerH.DeleteSchedule)
	}

	if cfg.InstallH != nil {
		mux.HandleFunc("GET /install.sh", cfg.InstallH.InstallScript)
		mux.HandleFunc("GET /download/client", cfg.InstallH.DownloadClient)
		mux.HandleFunc("POST /api/client/update", cfg.InstallH.TriggerUpdate)
	}

	if cfg.Snapshots != nil {
		mux.HandleFunc("GET /api/snapshots", cfg.Snapshots.List)
		mux.HandleFunc("POST /api/snapshots", cfg.Snapshots.StartSnapshot)
		mux.HandleFunc("POST /api/snapshots/{id}/end", cfg.Snapshots.EndSnapshot)
		mux.HandleFunc("DELETE /api/snapshots/{id}", cfg.Snapshots.DeleteSnapshot)
		mux.HandleFunc("GET /api/snapshots/{id}/ops", cfg.Snapshots.Ops)
		mux.HandleFunc("DELETE /api/snapshots/{id}/ops/{opID}", cfg.Snapshots.DeleteOp)
	}

	mux.HandleFunc("GET /ws/broadcast", service.BroadcastWS.Serve)
	mux.HandleFunc("GET /ws/admin", cfg.AdminWSH.Serve)
	mux.HandleFunc("GET /ws/terminal/{id}", cfg.TerminalWSH.Serve)

	webSubFS, err := fs.Sub(webFS, "web")
	if err != nil {
		panic("failed to get web sub filesystem: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(webSubFS))
	// Disable browser caching so embedded asset updates take effect immediately.
	noCacheFS := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		fileServer.ServeHTTP(w, r)
	})

	// Broadcast display pages — serve .html without extension for clean URLs.
	broadcastFS := noCacheFS
	// Cloud room mirror mode: the same SPA is served for /room/<id> and the
	// frontend reads the room id from the path.
	indexHTML, indexErr := fs.ReadFile(webSubFS, "index.html")
	roomPage := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if indexErr != nil {
			http.Error(w, "index unavailable", 500)
			return
		}
		_, _ = w.Write(indexHTML)
	}
	mux.HandleFunc("GET /room/{room}", roomPage)
	mux.HandleFunc("GET /room/{room}/", roomPage)
	mux.HandleFunc("GET /broadcast/before", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/broadcast/before.html"
		broadcastFS.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /broadcast/contesting", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/broadcast/contesting.html"
		broadcastFS.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /broadcast/after", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/broadcast/after.html"
		broadcastFS.ServeHTTP(w, r)
	})

	mux.Handle("GET /", noCacheFS)

	var handler http.Handler = mux
	ctx, stopBackground := context.WithCancel(context.Background())
	if cfg.Snapshots != nil {
		cfg.Snapshots.Start(ctx)
	}
	if cfg.Federation != nil {
		cfg.Federation.ConfigureBroadcast(cfg.BroadcastH, cfg.BackupH)
		cfg.Federation.Start(ctx, mux)
	}
	if cfg.AuthH != nil {
		handler = cfg.AuthH.AuthMiddleware(mux)
	}
	if cfg.Federation != nil {
		handler = cfg.Federation.NodeAuth(handler)
	}
	if cfg.BackupH != nil {
		handler = cfg.BackupH.Guard(handler)
	}
	handler = Recovery(Logger(handler))

	return &Server{
		stopBackground: stopBackground,
		httpServer: &http.Server{
			Addr:         ":" + cfg.Port,
			Handler:      handler,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 0, // allow long-lived WS / screen proxy streams
			IdleTimeout:  120 * time.Second,
		},
		bindIP:      cfg.BindIP,
		enableAvahi: cfg.Avahi,
	}
}

// Start begins listening and handles graceful shutdown.
func (s *Server) Start() error {
	defer s.stopBackground()
	if s.enableAvahi {
		go s.startAvahi()
	} else {
		log.Println("[avahi] disabled by config")
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("[server] shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(ctx); err != nil {
			log.Printf("[server] shutdown error: %v", err)
		}
		s.stopAvahi()
	}()

	log.Printf("[server] listening on :%s", s.httpServer.Addr[1:])
	if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	log.Println("[server] stopped")
	return nil
}

func (s *Server) startAvahi() {
	path, err := exec.LookPath("avahi-publish")
	if err != nil {
		log.Println("[avahi] avahi-publish not found, mDNS publishing disabled")
		return
	}

	ip := s.bindIP
	if ip == "" {
		ip = "0.0.0.0"
	}
	log.Printf("[avahi] publishing icpc-server.local on %s via mDNS", ip)
	s.avahiCmd = exec.Command(path, "-a", "-R", "icpc-server.local", ip)
	s.avahiCmd.Stdout = log.Writer()
	s.avahiCmd.Stderr = log.Writer()

	if err := s.avahiCmd.Start(); err != nil {
		log.Printf("[avahi] failed to start avahi-publish: %v", err)
		s.avahiCmd = nil
	}
}

func (s *Server) stopAvahi() {
	if s.avahiCmd != nil && s.avahiCmd.Process != nil {
		log.Println("[avahi] stopping avahi-publish")
		_ = s.avahiCmd.Process.Signal(syscall.SIGTERM)
		// Avoid zombie avahi-publish
		go s.avahiCmd.Wait()
	}
}

// ListInterfaces prints available network interfaces and their IPs for user selection.
func ListInterfaces() {
	interfaces, err := net.Interfaces()
	if err != nil {
		return
	}
	fmt.Println("Available network interfaces:")
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				fmt.Printf("  %s: %s\n", iface.Name, ipnet.IP.String())
			}
		}
	}
}

// AutoDetectIP returns a single IP if exactly one non-loopback interface is available.
func AutoDetectIP() string {
	var ips []string
	interfaces, err := net.Interfaces()
	if err != nil {
		return "0.0.0.0"
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}
	if len(ips) == 1 {
		return ips[0]
	}
	return "0.0.0.0"
}
