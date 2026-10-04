package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportToolResultMarkdown(t *testing.T) {
	for _, write := range []bool{false, true} {
		root := t.TempDir()
		if err := saveFinding(root, Finding{ID: "f_1", Title: "Finding", Severity: "high", Status: "open", Evidence: "**evidence**"}); err != nil {
			t.Fatal(err)
		}
		report, err := renderReport(root)
		if err != nil {
			t.Fatal(err)
		}
		result := reportToolResult(root, write)
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("unexpected result: %+v", result)
		}
		block := result.Content[0]
		if block.Type != "text" || block.Format != "markdown" || !strings.HasPrefix(block.Text, report) {
			t.Fatalf("report did not opt into Markdown: %+v", block)
		}
		if write {
			files, err := filepath.Glob(filepath.Join(root, stateDirName, "reports", "*.md"))
			if err != nil || len(files) != 1 {
				t.Fatalf("saved reports: %v, %v", files, err)
			}
			saved, err := os.ReadFile(files[0])
			if err != nil || string(saved) != report {
				t.Fatalf("saved Markdown changed: %q, %v", saved, err)
			}
		}
	}
}
