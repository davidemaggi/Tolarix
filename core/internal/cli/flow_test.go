package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/datadir"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

// setupCLI writes a master vault, an empty mirror vault and an import file.
func setupCLI(t *testing.T) (dataDir, importFile, mirror string) {
	t.Helper()
	t.Setenv("TOLARIX_SECRET_KEY", "")
	root := t.TempDir()
	dataDir = filepath.Join(root, "data")
	master := filepath.Join(root, "master")
	mirror = filepath.Join(root, "mirror")
	for p, c := range map[string]string{
		"Home.md":        "# Home\nSee [[Guide]].\n",
		"Docs.md":        "# Docs\n",
		"Docs/Guide.md":  "# Guide\n![[pic.png]]\n",
		"Docs/pic.png":   "PNG",
		".obsidian/x.md": "hidden",
	} {
		full := filepath.Join(master, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(mirror, 0o755)
	importFile = filepath.Join(root, "flows.yaml")
	doc := `connections:
  - name: Notes
    kind: obsidian
    config: { path: master }
  - name: Team
    kind: obsidian
    config: { path: mirror }
flows:
  - name: Notes to Team
    master: Notes
    mirrors:
      - connection: Team
        location: { folder: Mirror }
    options: { deletePolicy: delete }
`
	if err := os.WriteFile(importFile, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return dataDir, importFile, mirror
}

func TestFlowCommandsEndToEnd(t *testing.T) {
	data, file, mirror := setupCLI(t)
	dd := []string{"--data-dir", data}
	cmd := func(args ...string) (int, string, string) {
		t.Helper()
		return run(t, append(args, dd...)...)
	}

	if code, out, errs := cmd("flow", "import", file); code != ExitOK || !strings.Contains(out, `flow "Notes to Team" created`) {
		t.Fatalf("import: %d %s %s", code, out, errs)
	}
	// Importing again updates in place.
	if code, out, _ := cmd("flow", "import", file); code != ExitOK || !strings.Contains(out, "updated") {
		t.Fatalf("re-import: %d %s", code, out)
	}

	code, out, _ := cmd("flow", "list", "--json")
	var flows []map[string]any
	if code != ExitOK || json.Unmarshal([]byte(out), &flows) != nil || len(flows) != 1 || flows[0]["master"] != "Notes" {
		t.Fatalf("flow list: %d %s", code, out)
	}
	if code, out, _ := cmd("connection", "test", "team"); code != ExitOK || !strings.Contains(out, "works") {
		t.Fatalf("connection test: %d %s", code, out)
	}

	// Dry-run: plan printed, nothing written.
	code, out, _ = cmd("flow", "run", "Notes to Team", "--dry-run")
	if code != ExitOK || !strings.Contains(out, "would be 3 created") || !strings.Contains(out, "Mirror/Docs/Guide.md") {
		t.Fatalf("dry-run: %d\n%s", code, out)
	}
	if entries, _ := os.ReadDir(mirror); len(entries) != 0 {
		t.Fatal("dry-run wrote to the mirror")
	}

	code, out, _ = cmd("flow", "run", "notes to team", "--json")
	var res []runOutput
	if code != ExitOK || json.Unmarshal([]byte(out), &res) != nil || res[0].Run.Status != "succeeded" || res[0].Report.Counts.Created != 3 {
		t.Fatalf("run: %d %s", code, out)
	}
	guide, err := os.ReadFile(filepath.Join(mirror, "Mirror", "Docs", "Guide.md"))
	if err != nil || !strings.Contains(string(guide), "![[Mirror/attachments/pic.png]]") {
		t.Fatalf("mirror content: %v %q", err, guide)
	}

	if code, out, _ := cmd("runs", "list"); code != ExitOK || strings.Count(out, "Notes to Team") != 2 {
		t.Fatalf("runs list: %d %s", code, out)
	}
	if code, out, _ := cmd("runs", "show", shortID(res[0].Run.ID), "--log"); code != ExitOK || !strings.Contains(out, "create") || !strings.Contains(out, "run finished") {
		t.Fatalf("runs show: %d %s", code, out)
	}
	if code, _, _ := cmd("flow", "run", "--all"); code != ExitOK {
		t.Fatalf("run --all: %d", code)
	}

	// Error exit codes.
	if code, _, _ := cmd("flow", "run", "nope"); code != ExitNotFound {
		t.Errorf("unknown flow: %d", code)
	}
	if code, _, _ := cmd("flow", "run"); code != ExitUsage {
		t.Errorf("missing arg: %d", code)
	}
	if code, _, _ := cmd("runs", "show", "nope"); code != ExitNotFound {
		t.Errorf("unknown run: %d", code)
	}

	// A lock held elsewhere -> exit code 4.
	ctx := context.Background()
	st, err := store.Open(ctx, datadir.DatabasePath(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := st.GetFlow(ctx, "Notes to Team")
	if _, err := st.AcquireLock(ctx, f.ID, "elsewhere", "cron", time.Minute); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if code, _, errs := cmd("flow", "run", "Notes to Team"); code != ExitLocked || !strings.Contains(errs, "already running") {
		t.Errorf("locked: %d %s", code, errs)
	}

	// A broken master -> failure exit code 1.
	st, _ = store.Open(ctx, datadir.DatabasePath(data), nil)
	_ = st.ReleaseLock(ctx, f.ID, "elsewhere")
	_ = st.Close()
	if err := os.RemoveAll(filepath.Join(filepath.Dir(file), "master")); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := cmd("flow", "run", "Notes to Team"); code != ExitFailure {
		t.Errorf("failed run: %d", code)
	}
	if code, _, _ := cmd("connection", "test", "Notes"); code != ExitFailure {
		t.Errorf("failed connection test: %d", code)
	}
}

func TestWorseExit(t *testing.T) {
	if worseExit(ExitPartial, ExitFailure) != ExitFailure || worseExit(ExitLocked, ExitFailure) != ExitLocked || worseExit(ExitOK, ExitPartial) != ExitPartial {
		t.Fatal("exit code ordering")
	}
}
