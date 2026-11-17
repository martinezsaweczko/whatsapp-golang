package kiosk

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func newPurgeService(t *testing.T, dirs map[string]string) *Service {
	t.Helper()
	var cats []Category
	for name, dir := range dirs {
		cats = append(cats, Category{Name: name, Dir: dir, Retention: 24 * time.Hour})
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := New(cats, "files.example.com:443", &fakeUsageStore{}, &fakeIssuer{}, log,
		tracenoop.NewTracerProvider(), noop.NewMeterProvider())
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}
	return svc
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return err == nil
}

func TestPurgeOld(t *testing.T) {
	const maxAge = 30 * 24 * time.Hour

	tests := []struct {
		name string
		// setup creates the fixtures in dir and returns the paths expected
		// to be deleted and kept.
		setup       func(t *testing.T, dir string) (deleted, kept []string)
		wantDeleted int
	}{
		{
			name: "old file deleted, recent kept",
			setup: func(t *testing.T, dir string) ([]string, []string) {
				writeFileWithAge(t, dir, "old.pdf", maxAge+time.Hour)
				writeFileWithAge(t, dir, "recent.pdf", maxAge-time.Hour)
				return []string{filepath.Join(dir, "old.pdf")}, []string{filepath.Join(dir, "recent.pdf")}
			},
			wantDeleted: 1,
		},
		{
			name: "non-pdf regular files are deleted too",
			setup: func(t *testing.T, dir string) ([]string, []string) {
				writeFileWithAge(t, dir, "a.epub", maxAge+time.Hour)
				writeFileWithAge(t, dir, "b.txt", maxAge+48*time.Hour)
				return []string{filepath.Join(dir, "a.epub"), filepath.Join(dir, "b.txt")}, nil
			},
			wantDeleted: 2,
		},
		{
			name: "old subdirectory and its content are untouched",
			setup: func(t *testing.T, dir string) ([]string, []string) {
				sub := filepath.Join(dir, "sub")
				if err := os.Mkdir(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				writeFileWithAge(t, sub, "nested.pdf", maxAge+time.Hour)
				old := time.Now().Add(-maxAge - time.Hour)
				if err := os.Chtimes(sub, old, old); err != nil {
					t.Fatal(err)
				}
				return nil, []string{sub, filepath.Join(sub, "nested.pdf")}
			},
			wantDeleted: 0,
		},
		{
			name: "symlink to an old file is neither followed nor deleted",
			setup: func(t *testing.T, dir string) ([]string, []string) {
				outside := t.TempDir()
				writeFileWithAge(t, outside, "target.pdf", maxAge+time.Hour)
				target := filepath.Join(outside, "target.pdf")
				link := filepath.Join(dir, "link.pdf")
				if err := os.Symlink(target, link); err != nil {
					t.Skipf("symlinks unsupported: %v", err)
				}
				return nil, []string{link, target}
			},
			wantDeleted: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			deleted, kept := tt.setup(t, dir)
			svc := newPurgeService(t, map[string]string{"nacional": dir})

			n, err := svc.PurgeOld(context.Background(), maxAge)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n != tt.wantDeleted {
				t.Errorf("deleted count = %d, want %d", n, tt.wantDeleted)
			}
			for _, p := range deleted {
				if exists(t, p) {
					t.Errorf("%s should have been deleted", p)
				}
			}
			for _, p := range kept {
				if !exists(t, p) {
					t.Errorf("%s should have been kept", p)
				}
			}
		})
	}
}

func TestPurgeOldMissingFolderDoesNotAbortOthers(t *testing.T) {
	const maxAge = 30 * 24 * time.Hour
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeFileWithAge(t, dirA, "old-a.pdf", maxAge+time.Hour)
	writeFileWithAge(t, dirB, "old-b.pdf", maxAge+time.Hour)
	writeFileWithAge(t, dirB, "new-b.pdf", time.Hour)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	svc := newPurgeService(t, map[string]string{
		"nacional":      dirA,
		"internacional": missing,
		"magazine":      dirB,
	})

	n, err := svc.PurgeOld(context.Background(), maxAge)
	if err == nil {
		t.Fatal("expected an error for the missing folder")
	}
	if n != 2 {
		t.Errorf("deleted count = %d, want 2", n)
	}
	if exists(t, filepath.Join(dirA, "old-a.pdf")) || exists(t, filepath.Join(dirB, "old-b.pdf")) {
		t.Error("old files in readable folders should have been deleted")
	}
	if !exists(t, filepath.Join(dirB, "new-b.pdf")) {
		t.Error("recent file should have been kept")
	}
}

func TestPurgeOldRejectsNonPositiveMaxAge(t *testing.T) {
	dir := t.TempDir()
	writeFileWithAge(t, dir, "recent.pdf", time.Minute)
	svc := newPurgeService(t, map[string]string{"nacional": dir})

	if _, err := svc.PurgeOld(context.Background(), 0); err == nil {
		t.Fatal("expected an error for a zero max age")
	}
	if !exists(t, filepath.Join(dir, "recent.pdf")) {
		t.Error("no file must be deleted with an invalid max age")
	}
}
