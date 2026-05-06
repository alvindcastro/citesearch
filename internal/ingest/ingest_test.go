package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDryRun_ReturnsFileList_NoEmbedCalls(t *testing.T) {
	root := t.TempDir()
	docsPath := filepath.Join(root, "data", "docs", "banner", "general", "releases", "2026")
	if err := os.MkdirAll(docsPath, 0o755); err != nil {
		t.Fatalf("mkdir docs path: %v", err)
	}

	filePath := filepath.Join(docsPath, "Banner_General_Release_Notes_9.3.37.2.md")
	if err := os.WriteFile(filePath, []byte("Banner General release notes body"), 0o644); err != nil {
		t.Fatalf("write markdown file: %v", err)
	}

	report, err := dryRunReport(filepath.Join(root, "data", "docs", "banner"))
	if err != nil {
		t.Fatalf("dryRunReport: %v", err)
	}

	if !report.DryRun {
		t.Fatal("expected DryRun=true")
	}
	if len(report.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(report.Files))
	}

	file := report.Files[0]
	if file.Path != filePath {
		t.Fatalf("path: got %q, want %q", file.Path, filePath)
	}
	if file.SourceType != "banner" {
		t.Fatalf("source type: got %q, want %q", file.SourceType, "banner")
	}
	if file.Module != "General" {
		t.Fatalf("module: got %q, want %q", file.Module, "General")
	}
	if file.Version != "9.3.37.2" {
		t.Fatalf("version: got %q, want %q", file.Version, "9.3.37.2")
	}
	if file.Year != "2026" {
		t.Fatalf("year: got %q, want %q", file.Year, "2026")
	}
	if file.Pages != 1 {
		t.Fatalf("pages: got %d, want %d", file.Pages, 1)
	}
	if file.EstimatedChunks != 2 {
		t.Fatalf("estimated chunks: got %d, want %d", file.EstimatedChunks, 2)
	}
	if file.EstimatedSeconds != 1 {
		t.Fatalf("estimated seconds: got %d, want %d", file.EstimatedSeconds, 1)
	}
	if len(file.Warnings) != 0 {
		t.Fatalf("warnings: got %v, want none", file.Warnings)
	}

	if report.Totals.Files != 1 {
		t.Fatalf("total files: got %d, want %d", report.Totals.Files, 1)
	}
	if report.Totals.Pages != 1 {
		t.Fatalf("total pages: got %d, want %d", report.Totals.Pages, 1)
	}
	if report.Totals.EstimatedChunks != 2 {
		t.Fatalf("total chunks: got %d, want %d", report.Totals.EstimatedChunks, 2)
	}
	if report.Totals.EstimatedMinutes != 1 {
		t.Fatalf("total minutes: got %d, want %d", report.Totals.EstimatedMinutes, 1)
	}
}

func TestDryRun_DetectsModuleFromPath(t *testing.T) {
	root := t.TempDir()
	docsPath := filepath.Join(root, "data", "docs", "banner", "finance", "releases", "2026")
	if err := os.MkdirAll(docsPath, 0o755); err != nil {
		t.Fatalf("mkdir docs path: %v", err)
	}

	filePath := filepath.Join(docsPath, "Banner_Finance_Release_Notes_9.3.22.txt")
	if err := os.WriteFile(filePath, []byte("Finance release notes"), 0o644); err != nil {
		t.Fatalf("write text file: %v", err)
	}

	report, err := dryRunReport(filepath.Join(root, "data", "docs", "banner"))
	if err != nil {
		t.Fatalf("dryRunReport: %v", err)
	}

	if len(report.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(report.Files))
	}
	if report.Files[0].Module != "Finance" {
		t.Fatalf("module: got %q, want %q", report.Files[0].Module, "Finance")
	}
}

func TestDryRun_WarnsMissingModule(t *testing.T) {
	root := t.TempDir()
	docsPath := filepath.Join(root, "data", "docs", "banner", "releases", "2026")
	if err := os.MkdirAll(docsPath, 0o755); err != nil {
		t.Fatalf("mkdir docs path: %v", err)
	}

	filePath := filepath.Join(docsPath, "Banner_Release_Notes_9.3.22.txt")
	if err := os.WriteFile(filePath, []byte("Release notes"), 0o644); err != nil {
		t.Fatalf("write text file: %v", err)
	}

	report, err := dryRunReport(filepath.Join(root, "data", "docs", "banner"))
	if err != nil {
		t.Fatalf("dryRunReport: %v", err)
	}

	warnings := report.Files[0].Warnings
	if !containsString(warnings, "module not detected — check folder name") {
		t.Fatalf("expected missing module warning, got %v", warnings)
	}
}

func TestDryRun_WarnsSopNamingMismatch(t *testing.T) {
	root := t.TempDir()
	docsPath := filepath.Join(root, "data", "docs", "sop")
	if err := os.MkdirAll(docsPath, 0o755); err != nil {
		t.Fatalf("mkdir docs path: %v", err)
	}

	filePath := filepath.Join(docsPath, "Smoke Test Procedure.docx")
	if err := os.WriteFile(filePath, []byte("not a real docx, dry run should not open it"), 0o644); err != nil {
		t.Fatalf("write docx placeholder: %v", err)
	}

	report, err := dryRunReport(filepath.Join(root, "data", "docs"))
	if err != nil {
		t.Fatalf("dryRunReport: %v", err)
	}

	if len(report.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(report.Files))
	}

	file := report.Files[0]
	if file.SourceType != "sop" {
		t.Fatalf("source type: got %q, want %q", file.SourceType, "sop")
	}
	if !containsString(file.Warnings, "SOP naming mismatch — file will be skipped") {
		t.Fatalf("expected SOP warning, got %v", file.Warnings)
	}
	if containsString(file.Warnings, "module not detected — check folder name") {
		t.Fatalf("did not expect banner module warning for SOP file: %v", file.Warnings)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
