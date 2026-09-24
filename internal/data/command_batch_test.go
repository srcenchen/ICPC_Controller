package data

import (
	"path/filepath"
	"testing"

	"ICPCRemoteControl/internal/model"
)

func TestCreateChildrenUsesOneInsert(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "commands.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewCommandRepo(db)
	parent := &model.CommandLog{TargetType: "list", Command: "hostname", Status: model.CommandStatusDispatched, ExecutedBy: "test"}
	if err := repo.Create(parent); err != nil {
		t.Fatal(err)
	}
	first, second := 3, 9
	children := []*model.CommandLog{
		{TargetID: &first, Command: "hostname", Status: model.CommandStatusDispatched, DispatchedAt: parent.CreatedAt, ExecutedBy: "test"},
		{TargetID: &second, Command: "hostname", Status: model.CommandStatusFailed, ErrorOutput: "offline", CompletedAt: parent.CreatedAt, ExecutedBy: "test"},
	}
	if err := repo.CreateChildren(parent.ID, children); err != nil {
		t.Fatal(err)
	}
	if children[0].ID == 0 || children[1].ID != children[0].ID+1 {
		t.Fatalf("child ids not consecutive: %d %d", children[0].ID, children[1].ID)
	}
	loaded, err := repo.GetByParentID(parent.ID)
	if err != nil || len(loaded) != 2 || loaded[0].Status != model.CommandStatusDispatched || loaded[1].ErrorOutput != "offline" {
		t.Fatalf("children not stored: %+v %v", loaded, err)
	}
}
