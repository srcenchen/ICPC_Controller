package biz

import (
	"log"
	"time"

	"ICPCRemoteControl/internal/data"
)

// janitorInterval is how often retention and schedule checks run. Power
// schedules therefore fire within a minute of their configured time.
const janitorInterval = time.Minute

// MaintenanceConfig is the subset of server settings the janitor needs.
// Defined here (rather than importing service) to keep biz free of that dependency.
type MaintenanceConfig struct {
	CmdRetentionDays  int
	CmdRetentionMax   int
	EventRetentionDay int
}

// MaintenanceProvider supplies current retention configuration.
type MaintenanceProvider interface {
	MaintenanceForJanitor() MaintenanceConfig
}

// ScheduleRunner executes a due power schedule.
type ScheduleRunner interface {
	RunSchedule(s data.PowerSchedule) error
}

// Janitor performs periodic housekeeping: command log retention, ghost device
// rows, device event trimming, and firing due power schedules.
type Janitor struct {
	commandRepo  *data.CommandRepo
	deviceRepo   *data.DeviceRepo
	eventRepo    *data.DeviceEventRepo
	scheduleRepo *data.PowerScheduleRepo
	settings     MaintenanceProvider
	runner       ScheduleRunner
	stopCh       chan struct{}
}

func NewJanitor(commandRepo *data.CommandRepo, deviceRepo *data.DeviceRepo,
	eventRepo *data.DeviceEventRepo, scheduleRepo *data.PowerScheduleRepo,
	settings MaintenanceProvider, runner ScheduleRunner) *Janitor {
	return &Janitor{
		commandRepo:  commandRepo,
		deviceRepo:   deviceRepo,
		eventRepo:    eventRepo,
		scheduleRepo: scheduleRepo,
		settings:     settings,
		runner:       runner,
		stopCh:       make(chan struct{}),
	}
}

func (j *Janitor) Start() {
	go func() {
		ticker := time.NewTicker(janitorInterval)
		defer ticker.Stop()
		// Retention pass runs hourly; schedules are checked every tick.
		lastRetention := time.Time{}
		for {
			select {
			case <-j.stopCh:
				return
			case now := <-ticker.C:
				j.fireDueSchedules(now)
				if now.Sub(lastRetention) >= time.Hour {
					lastRetention = now
					j.runRetention(now)
				}
			}
		}
	}()
	log.Printf("[janitor] started (interval %s)", janitorInterval)
}

func (j *Janitor) Stop() {
	select {
	case <-j.stopCh:
	default:
		close(j.stopCh)
	}
}

func (j *Janitor) runRetention(now time.Time) {
	cfg := j.settings.MaintenanceForJanitor()

	if cfg.CmdRetentionDays > 0 {
		cutoff := now.AddDate(0, 0, -cfg.CmdRetentionDays)
		if n, err := j.commandRepo.DeleteOlderThan(cutoff); err != nil {
			log.Printf("[janitor] command retention by age: %v", err)
		} else if n > 0 {
			log.Printf("[janitor] deleted %d command log row(s) older than %d days", n, cfg.CmdRetentionDays)
		}
	}
	if cfg.CmdRetentionMax > 0 {
		if n, err := j.commandRepo.DeleteBeyondCount(cfg.CmdRetentionMax); err != nil {
			log.Printf("[janitor] command retention by count: %v", err)
		} else if n > 0 {
			log.Printf("[janitor] trimmed %d command log row(s) beyond %d records", n, cfg.CmdRetentionMax)
		}
	}
	if cfg.EventRetentionDay > 0 && j.eventRepo != nil {
		cutoff := now.AddDate(0, 0, -cfg.EventRetentionDay)
		if n, err := j.eventRepo.DeleteOlderThan(cutoff); err != nil {
			log.Printf("[janitor] device event retention: %v", err)
		} else if n > 0 {
			log.Printf("[janitor] deleted %d device event row(s)", n)
		}
		if n, err := j.scheduleRepo.DeleteOlderThan(cutoff); err != nil {
			log.Printf("[janitor] power schedule retention: %v", err)
		} else if n > 0 {
			log.Printf("[janitor] deleted %d finished power schedule(s)", n)
		}
	}
	if n, err := j.deviceRepo.DeleteGhostRows(); err != nil {
		log.Printf("[janitor] ghost row sweep: %v", err)
	} else if n > 0 {
		log.Printf("[janitor] removed %d incomplete device row(s)", n)
	}
}

func (j *Janitor) fireDueSchedules(now time.Time) {
	if j.scheduleRepo == nil || j.runner == nil {
		return
	}
	due, err := j.scheduleRepo.DuePending(now)
	if err != nil {
		log.Printf("[janitor] load due schedules: %v", err)
		return
	}
	for _, s := range due {
		log.Printf("[janitor] firing power schedule #%d (%s, %s)", s.ID, s.Action, s.TargetType)
		if err := j.runner.RunSchedule(s); err != nil {
			log.Printf("[janitor] schedule #%d failed: %v", s.ID, err)
			_ = j.scheduleRepo.SetStatus(s.ID, data.ScheduleStatusFailed, err.Error())
			continue
		}
		_ = j.scheduleRepo.SetStatus(s.ID, data.ScheduleStatusFired, "")
	}
}
