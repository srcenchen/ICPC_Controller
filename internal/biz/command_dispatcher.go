package biz

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"ICPCRemoteControl/internal/data"
	"ICPCRemoteControl/internal/model"
)

// CommandDispatcher handles dispatching commands to client connections.
type CommandDispatcher struct {
	hub         *Hub
	commandRepo *data.CommandRepo
}

// NewCommandDispatcher creates a new CommandDispatcher.
func NewCommandDispatcher(hub *Hub, commandRepo *data.CommandRepo) *CommandDispatcher {
	return &CommandDispatcher{hub: hub, commandRepo: commandRepo}
}

// CreateAndDispatch persists a command then dispatches it (single or broadcast).
// Used by non-HTTP callers such as the scheduled power actions.
func (d *CommandDispatcher) CreateAndDispatch(cmd *model.CommandLog) error {
	if err := d.commandRepo.Create(cmd); err != nil {
		return fmt.Errorf("create command: %w", err)
	}
	if cmd.TargetType == "broadcast" {
		return d.DispatchBroadcast(cmd)
	}
	if cmd.TargetID == nil {
		return fmt.Errorf("single command requires a target device")
	}
	return d.DispatchSingle(cmd)
}

// DispatchSingle sends a command to a specific device.
func (d *CommandDispatcher) DispatchSingle(parentCmd *model.CommandLog) error {
	client := d.hub.GetClient(*parentCmd.TargetID)
	if client == nil {
		parentCmd.Status = model.CommandStatusFailed
		parentCmd.ErrorOutput = fmt.Sprintf("device %d is not connected", *parentCmd.TargetID)
		d.commandRepo.UpdateStatus(parentCmd)
		return fmt.Errorf("device %d not connected", *parentCmd.TargetID)
	}

	return d.dispatchToClient(client, parentCmd)
}

// DispatchBroadcast creates one child per connected device in a single insert, then sends.
func (d *CommandDispatcher) DispatchBroadcast(parentCmd *model.CommandLog) error {
	ids := d.hub.ConnectedIDs()
	if len(ids) == 0 {
		parentCmd.Status = model.CommandStatusFailed
		parentCmd.ErrorOutput = "no devices connected"
		d.commandRepo.UpdateStatus(parentCmd)
		return fmt.Errorf("no devices connected")
	}
	children, err := d.Materialize(parentCmd, ids)
	if err != nil {
		return err
	}
	d.SendPrepared(parentCmd.ID, children)
	return nil
}

// Materialize writes one child row per device. Connected devices are stored as
// already dispatched so the send loop does not rewrite each row.
func (d *CommandDispatcher) Materialize(parent *model.CommandLog, ids []int) ([]*model.CommandLog, error) {
	now := parent.CreatedAt
	if now == "" {
		now = parent.DispatchedAt
	}
	children := make([]*model.CommandLog, 0, len(ids))
	seen := make(map[int]bool, len(ids))
	online := make(map[int]bool, len(ids))
	for _, id := range d.hub.ConnectedIDs() {
		online[id] = true
	}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		target := id
		child := &model.CommandLog{
			TargetType: "single",
			TargetID:   &target,
			Command:    parent.Command,
			ExecutedBy: parent.ExecutedBy,
		}
		if online[id] {
			child.Status = model.CommandStatusDispatched
			child.DispatchedAt = now
		} else {
			child.Status = model.CommandStatusFailed
			child.ErrorOutput = fmt.Sprintf("device %d is not connected", id)
			child.CompletedAt = now
		}
		children = append(children, child)
	}
	if len(children) == 0 {
		return nil, fmt.Errorf("no target devices")
	}
	if err := d.commandRepo.CreateChildren(parent.ID, children); err != nil {
		return nil, err
	}
	return children, nil
}

// SendPrepared pushes already-recorded child commands onto client sockets.
// A full send buffer is recorded on that child only.
func (d *CommandDispatcher) SendPrepared(parentID int64, children []*model.CommandLog) {
	for _, child := range children {
		if child.Status != model.CommandStatusDispatched || child.TargetID == nil {
			continue
		}
		client := d.hub.GetClient(*child.TargetID)
		if client == nil {
			child.Status = model.CommandStatusFailed
			child.ErrorOutput = fmt.Sprintf("device %d is not connected", *child.TargetID)
			d.commandRepo.UpdateStatus(child)
			continue
		}
		if err := d.dispatchToClient(client, child); err != nil {
			log.Printf("[dispatcher] device %d: %v", *child.TargetID, err)
		}
	}
	if commandBatchSettled(children) {
		d.UpdateBroadcastParentStatus(parentID)
	}
}

func commandBatchSettled(children []*model.CommandLog) bool {
	if len(children) == 0 {
		return false
	}
	for _, child := range children {
		switch child.Status {
		case model.CommandStatusCompleted, model.CommandStatusFailed, model.CommandStatusTimeout:
		default:
			return false
		}
	}
	return true
}

// UpdateBroadcastParentStatus checks if all children of a broadcast are done and updates the parent.
func (d *CommandDispatcher) UpdateBroadcastParentStatus(parentID int64) {
	children, err := d.commandRepo.GetByParentID(parentID)
	if err != nil {
		log.Printf("[dispatcher] get children for parent %d: %v", parentID, err)
		return
	}

	completed := 0
	failed := 0
	timedOut := 0
	var totalDuration int64
	var failedDevices []string

	for _, child := range children {
		switch child.Status {
		case model.CommandStatusCompleted:
			completed++
		case model.CommandStatusFailed:
			failed++
		case model.CommandStatusTimeout:
			timedOut++
		default:
			return
		}
		totalDuration += child.DurationMS
		if child.Status != model.CommandStatusCompleted && child.TargetID != nil {
			failedDevices = append(failedDevices, fmt.Sprintf("#%d", *child.TargetID))
		}
	}

	parent, err := d.commandRepo.GetByID(parentID)
	if err != nil {
		log.Printf("[dispatcher] get parent %d: %v", parentID, err)
		return
	}

	// Summary only: concatenating every child's stdout duplicated the whole
	// fleet's output into one row. The UI expands children on demand.
	summary := fmt.Sprintf("共 %d 台：成功 %d，失败 %d，超时 %d", len(children), completed, failed, timedOut)
	if len(failedDevices) > 0 {
		shown := failedDevices
		if len(shown) > 40 {
			shown = shown[:40]
		}
		summary += "\n未成功设备：" + strings.Join(shown, " ")
		if len(failedDevices) > len(shown) {
			summary += fmt.Sprintf(" …（另 %d 台）", len(failedDevices)-len(shown))
		}
	}
	summary += "\n（展开查看每台设备的详细输出）"

	parent.Status = model.CommandStatusCompleted
	parent.Output = summary
	parent.DurationMS = totalDuration
	if err := d.commandRepo.UpdateStatus(parent); err != nil {
		log.Printf("[dispatcher] update parent %d: %v", parentID, err)
	}

	log.Printf("[dispatcher] broadcast #%d done: %d completed, %d failed, %d timeout",
		parentID, completed, failed, timedOut)
}

func (d *CommandDispatcher) dispatchToClient(client *ClientConn, cmd *model.CommandLog) error {
	msg := model.ExecuteMessage{
		Type:      "execute",
		CommandID: cmd.ID,
		Command:   cmd.Command,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal execute message: %w", err)
	}
	data = append(data, '\n')

	select {
	case client.Send <- data:
	default:
		cmd.Status = model.CommandStatusFailed
		cmd.ErrorOutput = "send buffer full"
		d.commandRepo.UpdateStatus(cmd)
		return fmt.Errorf("send buffer full for device %d", client.AssignedID)
	}

	// Batch fan-out already stored dispatched rows. Skip the per-device rewrite.
	if cmd.Status == model.CommandStatusDispatched && cmd.DispatchedAt != "" {
		return nil
	}
	cmd.Status = model.CommandStatusDispatched
	if err := d.commandRepo.UpdateStatus(cmd); err != nil {
		log.Printf("[dispatcher] failed to update command status: %v", err)
	}
	return nil
}
