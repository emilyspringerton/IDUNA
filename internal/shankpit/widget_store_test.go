package shankpit_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/shankpit"
)

func newTestWidgetStore(t *testing.T) *shankpit.WidgetStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE shankpit_widgets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			walls_json TEXT NOT NULL DEFAULT '[]',
			doors_json TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create shankpit_widgets: %v", err)
	}
	return &shankpit.WidgetStore{DB: db}
}

func TestCreateWidget_EmptyWallsAndDoorsAllowed(t *testing.T) {
	s := newTestWidgetStore(t)
	w, err := s.CreateWidget(context.Background(), "Empty Widget", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(w.Walls) != 0 || len(w.Doors) != 0 {
		t.Fatalf("expected zero walls/doors, got %+v", w)
	}
}

func TestCreateWidget_RejectsInvalidName(t *testing.T) {
	s := newTestWidgetStore(t)
	if _, err := s.CreateWidget(context.Background(), "", nil, nil); err == nil {
		t.Fatal("expected an error for an empty name")
	}
}

func TestCreateWidget_RejectsDoorReferencingUnknownWall(t *testing.T) {
	s := newTestWidgetStore(t)
	if _, err := s.CreateWidget(context.Background(), "Door Widget", nil, []shankpit.Door{{WallID: 99, ScriptID: 0}}); err == nil {
		t.Fatal("expected an error for a door referencing a wall that doesn't exist in this widget")
	}
}

func TestGetWidget_RoundTripsWallsAndDoors(t *testing.T) {
	s := newTestWidgetStore(t)
	wall := shankpit.Wall{ID: 1, X: 0, Y: 0, Z: 0, SX: 3, SY: 8, SZ: 1, R: 0.5, G: 0.5, B: 0.5}
	created, err := s.CreateWidget(context.Background(), "Door Widget", []shankpit.Wall{wall}, []shankpit.Door{{WallID: 1, ScriptID: 0}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetWidget(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Walls) != 1 || len(got.Doors) != 1 {
		t.Fatalf("expected 1 wall and 1 door to round-trip, got %+v", got)
	}
}

func TestListWidgets_ReturnsCountsNotFullArrays(t *testing.T) {
	s := newTestWidgetStore(t)
	wall := shankpit.Wall{ID: 1, X: 0, Y: 0, Z: 0, SX: 3, SY: 8, SZ: 1, R: 0.5, G: 0.5, B: 0.5}
	if _, err := s.CreateWidget(context.Background(), "Door Widget", []shankpit.Wall{wall}, []shankpit.Door{{WallID: 1, ScriptID: 0}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	list, err := s.ListWidgets(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].WallCount != 1 || list[0].DoorCount != 1 {
		t.Fatalf("unexpected list result: %+v", list)
	}
}

func TestUpdateWidget_ReplacesWallsAndDoors(t *testing.T) {
	s := newTestWidgetStore(t)
	created, err := s.CreateWidget(context.Background(), "Widget", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wall := shankpit.Wall{ID: 1, X: 5, Y: 0, Z: 0, SX: 2, SY: 2, SZ: 2, R: 1, G: 1, B: 1}
	updated, err := s.UpdateWidget(context.Background(), created.ID, []shankpit.Wall{wall}, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(updated.Walls) != 1 || updated.Walls[0].X != 5 {
		t.Fatalf("expected the new wall to replace the old (empty) set, got %+v", updated.Walls)
	}
}

func TestDeleteWidget(t *testing.T) {
	s := newTestWidgetStore(t)
	created, err := s.CreateWidget(context.Background(), "Widget", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.DeleteWidget(context.Background(), created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetWidget(context.Background(), created.ID); err == nil {
		t.Fatal("expected widget to be gone after delete")
	}
}
