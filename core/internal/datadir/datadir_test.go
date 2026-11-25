package datadir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrecedence(t *testing.T) {
	envDir := t.TempDir()
	flagDir := t.TempDir()
	t.Setenv(EnvVar, envDir)

	got, err := Resolve(flagDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != flagDir {
		t.Errorf("flag should win: got %q want %q", got, flagDir)
	}

	got, err = Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if got != envDir {
		t.Errorf("env should win over default: got %q want %q", got, envDir)
	}

	t.Setenv(EnvVar, "")
	got, err = Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	def, _ := Default()
	if got != def {
		t.Errorf("default: got %q want %q", got, def)
	}
}

func TestResolveMakesAbsolute(t *testing.T) {
	got, err := Resolve("relative-dir")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

func TestEnsure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	if err := Ensure(dir); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("probe file left behind: %v", entries)
	}
}

func TestEnsureRejectsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(f); err == nil {
		t.Fatal("expected error for a file path")
	}
}
