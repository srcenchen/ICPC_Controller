package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/server"
	"ICPCRemoteControl/internal/service"
)

func main() {
	port := flag.String("port", "8080", "HTTP server port")
	tcpPort := flag.String("tcp-port", "8081", "TCP port for client connections")
	dbPath := flag.String("db", "icpc.db", "SQLite database path")
	bind := flag.String("bind", "", "IP address for avahi mDNS (auto-detect if empty)")
	avahi := flag.Bool("avahi", true, "enable avahi mDNS publishing")
	listIfaces := flag.Bool("list-interfaces", false, "list available network interfaces and exit")
	hostnamePrefix := flag.String("hostname-prefix", "cwxu-icpc", "hostname prefix for client machines (e.g. 'cwxu-icpc' → 'cwxu-icpc-1')")
	flag.Parse()

	if *listIfaces {
		server.ListInterfaces()
		os.Exit(0)
	}

	bindIP := *bind
	if bindIP == "" {
		bindIP = server.AutoDetectIP()
		if bindIP == "0.0.0.0" && *avahi {
			fmt.Println("Multiple or no network interfaces detected. Use --list-interfaces to see options.")
			fmt.Println("Specify --bind <ip> for mDNS publishing, or --avahi=false to disable.")
		} else {
			log.Printf("[main] auto-detected bind IP: %s", bindIP)
		}
	} else {
		log.Printf("[main] using specified bind IP: %s", bindIP)
	}

	db, err := data.NewDB(*dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	log.Printf("[main] database opened: %s", *dbPath)

	deviceRepo := data.NewDeviceRepo(db)
	commandRepo := data.NewCommandRepo(db)
	settingsRepo := data.NewSettingsRepo(db)
	eventRepo := data.NewDeviceEventRepo(db)
	scheduleRepo := data.NewPowerScheduleRepo(db)

	if err := deviceRepo.MarkAllOffline(); err != nil {
		log.Printf("[main] warning: failed to mark devices offline: %v", err)
	}
	// Sweep placeholder rows left by clients that registered then dropped
	// before reporting system info (crash-safety net for the deferred cleanup).
	if n, err := deviceRepo.DeleteGhostRows(); err != nil {
		log.Printf("[main] warning: ghost row sweep: %v", err)
	} else if n > 0 {
		log.Printf("[main] removed %d incomplete device row(s)", n)
	}

	settings := service.NewServerSettings(*hostnamePrefix, settingsRepo)

	idAssigner := biz.NewIDAssigner(deviceRepo)
	hub := biz.NewHub(deviceRepo)
	hub.SetEventRepo(eventRepo)
	dispatcher := biz.NewCommandDispatcher(hub, commandRepo)

	// HTTP + Admin WS.
	deviceHandler := service.NewDeviceHandler(deviceRepo, hub, eventRepo)
	commandHandler := service.NewCommandHandler(commandRepo, dispatcher, hub, settings)
	statsHandler := service.NewStatsHandler(deviceRepo, commandRepo)
	adminWSHandler := service.NewAdminWSHandler(hub)
	terminalWSH := service.NewTerminalWSHandler(hub)
	settingsHandler := service.NewSettingsHandler(settings, hub)
	broadcastRepo := data.NewBroadcastRepo(db)
	broadcastHandler := service.NewBroadcastHandler(broadcastRepo)
	networkHandler := service.NewNetworkHandler(settings, hub, commandRepo, dispatcher)
	checkinHandler := service.NewCheckinHandler(deviceRepo, hub)
	authHandler := service.NewAuthHandler(settings)

	service.DistributionMgr = service.NewDistributionManager(hub, "data/uploads")
	service.DistributionMgr.SetHostnameLookup(func(deviceID int) string {
		d, err := deviceRepo.GetByAssignedID(deviceID)
		if err != nil || d == nil {
			return ""
		}
		if d.Hostname != "" {
			return d.Hostname
		}
		if d.StudentName != "" {
			return d.StudentName
		}
		return ""
	})
	distHandler := service.NewDistributionHandler(service.DistributionMgr)
	screenProxyH := service.NewScreenProxyHandler(deviceRepo)

	powerHandler := service.NewPowerHandler(deviceRepo, dispatcher, scheduleRepo, hub, settings)
	installHandler := service.NewInstallHandler("data/uploads", *tcpPort, hub)

	// TCP handler for client connections.
	tcpHandler := service.NewTCPHandler(hub, deviceRepo, commandRepo, idAssigner, dispatcher, settings, broadcastRepo, eventRepo)

	// Background janitor: log retention, ghost rows, scheduled power actions.
	janitor := biz.NewJanitor(commandRepo, deviceRepo, eventRepo, scheduleRepo, settings, powerHandler)
	janitor.Start()
	defer janitor.Stop()

	// Start TCP listener in background.
	go func() {
		if err := service.StartTCPListener(":"+*tcpPort, tcpHandler); err != nil {
			log.Fatalf("[main] TCP listener failed: %v", err)
		}
	}()

	// Start HTTP server.
	cfg := server.Config{
		Port:          *port,
		BindIP:        bindIP,
		DBPath:        *dbPath,
		Avahi:         *avahi,
		DeviceH:       deviceHandler,
		CommandH:      commandHandler,
		StatsH:        statsHandler,
		AdminWSH:      adminWSHandler,
		TerminalWSH:   terminalWSH,
		SettingsH:     settingsHandler,
		NetworkH:      networkHandler,
		CheckinH:      checkinHandler,
		BroadcastH:    broadcastHandler,
		AuthH:         authHandler,
		DistributionH: distHandler,
		ScreenProxyH:  screenProxyH,
		PowerH:        powerHandler,
		InstallH:      installHandler,
	}

	srv := server.New(cfg)
	log.Printf("[main] HTTP on :%s, TCP on :%s", *port, *tcpPort)
	if err := srv.Start(); err != nil {
		log.Printf("[main] server error: %v", err)
		os.Exit(1)
	}
}
