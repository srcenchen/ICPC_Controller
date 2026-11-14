package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"syscall"

	"ICPCRemoteControl/internal/biz"
	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/server"
	"ICPCRemoteControl/internal/service"
)

func main() {
	port := flag.String("port", "8080", "HTTP 服务端口")
	tcpPort := flag.String("tcp-port", "8081", "选手机控制 TCP 端口")
	p2pPort := flag.String("p2p-port", "48080", "本地 P2P 发送端口")
	dbPath := flag.String("db", "icpc.db", "SQLite 数据库路径")
	bind := flag.String("bind", "", "mDNS 发布使用的本机 IP（启用 --avahi=true 时必填）")
	avahi := flag.Bool("avahi", false, "是否启用 avahi mDNS 发布 icpc-server.local（默认关闭；启用时必须用 --bind 指定 IP）")
	listIfaces := flag.Bool("list-interfaces", false, "列出可用网络接口及其 IP 后退出")
	hostnamePrefix := flag.String("hostname-prefix", "cwxu-icpc", "选手机主机名前缀（例如 'cwxu-icpc' → 'cwxu-icpc-1'）")
	restore := flag.String("restore", "", "在服务停止时恢复已验证的备份 ZIP，然后退出")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "用法：%s [参数]\n\n参数：\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if *listIfaces {
		server.ListInterfaces()
		os.Exit(0)
	}
	bindIP := *bind
	if *avahi {
		if bindIP == "" {
			fmt.Fprintln(os.Stderr, "错误：启用 mDNS（--avahi=true）时必须通过 --bind 指定本机 IP，例如：--bind 192.168.1.10")
			fmt.Fprintln(os.Stderr, "可用 --list-interfaces 查看网卡地址。")
			os.Exit(1)
		}
		ip := net.ParseIP(bindIP)
		if ip == nil || ip.IsUnspecified() {
			fmt.Fprintf(os.Stderr, "错误：--bind %q 不是可用的具体 IP 地址（不能是 0.0.0.0 或 ::）。\n", bindIP)
			fmt.Fprintln(os.Stderr, "可用 --list-interfaces 查看网卡地址。")
			os.Exit(1)
		}
		log.Printf("[main] mDNS 发布地址：%s", bindIP)
	} else {
		log.Println("[main] 未启用 mDNS（--avahi 默认关闭）")
	}

	lockFile, err := os.OpenFile(*dbPath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		log.Fatal("database is in use; stop the server before restoring or launching another instance")
	}
	if *restore != "" {
		if err := data.RestoreBackup(*restore, *dbPath, "data"); err != nil {
			log.Fatalf("restore failed: %v", err)
		}
		log.Println("backup restored; previous database and data retained with .pre-restore suffix; restart the server")
		return
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
	service.DistributionMgr.SetTransferPort(*p2pPort)
	service.DistributionMgr.SetTargetLookup(func() []int {
		devices, err := deviceRepo.GetAll()
		if err != nil {
			return nil
		}
		ids := make([]int, 0, len(devices))
		for _, device := range devices {
			ids = append(ids, device.AssignedID)
		}
		return ids
	})
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
	federation := service.NewFederation(db, settings, deviceRepo, service.DistributionMgr, hub)
	screenProxyH := service.NewScreenProxyHandler(deviceRepo)

	powerHandler := service.NewPowerHandler(deviceRepo, dispatcher, scheduleRepo, hub, settings)
	installHandler := service.NewInstallHandler("data/uploads", *tcpPort, hub)

	// Offline operation snapshots (queues) replayed to late devices/rooms.
	snapshotRepo := data.NewSnapshotRepo(db)
	snapshotMgr := service.NewSnapshotManager(snapshotRepo, settings, deviceRepo, hub, dispatcher)
	snapshotMgr.SetDistribution(service.DistributionMgr)
	snapshotMgr.SetPower(powerHandler)
	snapshotMgr.SetBroadcastRepo(broadcastRepo)
	commandHandler.SetSnapshotManager(snapshotMgr)
	networkHandler.SetSnapshotManager(snapshotMgr)
	powerHandler.SetSnapshotManager(snapshotMgr)
	distHandler.SetSnapshotManager(snapshotMgr)
	hub.SetConnectHook(snapshotMgr.ReplayToDevice)
	federation.SetSnapshotManager(snapshotMgr)
	snapshotMgr.SetFederation(federation)

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
		Federation:    federation,
		BackupH:       service.NewBackupHandler(db, settings),
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
		Snapshots:     snapshotMgr,
	}

	srv := server.New(cfg)
	log.Printf("[main] HTTP on :%s, TCP on :%s", *port, *tcpPort)
	if err := srv.Start(); err != nil {
		log.Printf("[main] server error: %v", err)
		os.Exit(1)
	}
}
