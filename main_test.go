package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseTriageArgs(t *testing.T) {
	id, status, note, err := parseTriageArgs("f_20260723161433_5828 false-positive not actionable here")
	if err != nil {
		t.Fatalf("parseTriageArgs returned an error: %v", err)
	}
	if id != "f_20260723161433_5828" || status != "false-positive" || note != "not actionable here" {
		t.Fatalf("unexpected result: id=%q status=%q note=%q", id, status, note)
	}

	if _, _, _, err := parseTriageArgs("f_1 dismissed"); err == nil {
		t.Fatal("parseTriageArgs accepted an invalid status")
	}
}

func TestTriageFinding(t *testing.T) {
	root := t.TempDir()
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	finding := Finding{
		ID:        "f_20260701120000_0001",
		Feature:   "auth",
		Title:     "example",
		Severity:  "high",
		Status:    "open",
		CreatedAt: created,
		UpdatedAt: created,
	}
	if err := saveFinding(root, finding); err != nil {
		t.Fatalf("saveFinding returned an error: %v", err)
	}

	got, err := triageFinding(root, finding.ID, "wontfix", "accepted risk")
	if err != nil {
		t.Fatalf("triageFinding returned an error: %v", err)
	}
	if got.Status != "wontfix" || len(got.Notes) != 1 || got.Notes[0].Text != "accepted risk" {
		t.Fatalf("unexpected triaged finding: %+v", got)
	}
	if !got.UpdatedAt.After(created) {
		t.Fatalf("updated_at was not advanced: %s", got.UpdatedAt)
	}

	persisted, err := loadFinding(root, finding.ID)
	if err != nil {
		t.Fatalf("loadFinding returned an error: %v", err)
	}
	if persisted.Status != got.Status || len(persisted.Notes) != 1 {
		t.Fatalf("triage was not persisted: %+v", persisted)
	}
}

func TestMapCustomFeaturesFromLines(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature.lua"), []byte("function app.start() end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestConfig(t, root, Config{
		MapFeaturesCommand: "printf 'feature.lua:1:function app.start()\\nvirtual:entry\\n'",
	})

	features, err := mapCustomFeatures(root)
	if err != nil {
		t.Fatalf("mapCustomFeatures returned an error: %v", err)
	}
	if len(features) != 2 {
		t.Fatalf("got %d features, want 2: %+v", len(features), features)
	}
	if features[0].Name != "custom:1" || features[0].Description != "feature.lua:1:function app.start()" {
		t.Fatalf("unexpected first feature: %+v", features[0])
	}
	if len(features[0].Roots) != 1 || features[0].Roots[0] != "feature.lua" {
		t.Fatalf("existing path was not detected: %+v", features[0].Roots)
	}
	if len(features[1].Roots) != 0 {
		t.Fatalf("nonexistent path was included: %+v", features[1].Roots)
	}
}

func TestMapCustomFeaturesFromJSON(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, Config{
		MapFeaturesCommand: `printf '[{"name":"lua:app","kind":"module","roots":["app"]}]'`,
	})

	features, err := mapCustomFeatures(root)
	if err != nil {
		t.Fatalf("mapCustomFeatures returned an error: %v", err)
	}
	if len(features) != 1 || features[0].Name != "lua:app" || features[0].Kind != "module" {
		t.Fatalf("unexpected features: %+v", features)
	}
}

func TestLoadFindingRejectsUnsafeID(t *testing.T) {
	if _, err := loadFinding(t.TempDir(), "../../outside"); err == nil {
		t.Fatal("loadFinding accepted a path as an id")
	}
}

func writeTestConfig(t *testing.T, root string, cfg Config) {
	t.Helper()
	dir := filepath.Join(root, stateDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), content, 0o644); err != nil {
		t.Fatal(err)
	}
}
