package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/datadir"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

func TestRunDue(t *testing.T) {
	data, file, _ := setupCLI(t)
	dd := []string{"--data-dir", data}
	cmd := func(args ...string) (int, string, string) {
		t.Helper()
		return run(t, append(args, dd...)...)
	}
	// Schedule from the import file, with the built-in scheduler off.
	b, _ := os.ReadFile(file)
	doc := string(b) + "    schedule: { kind: interval, every: 30, unit: minutes }\n    timezone: Europe/Rome\n    builtInSchedule: false\n"
	if err := os.WriteFile(file, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := cmd("flow", "import", file); code != ExitOK {
		t.Fatalf("import: %d %s %s", code, out, errs)
	}
	code, out, _ := cmd("flow", "list")
	if code != ExitOK || !strings.Contains(out, "Every 30 minutes, Europe/Rome (run-due only)") {
		t.Fatalf("flow list: %s", out)
	}

	// Just created: nothing is due yet.
	if code, out, _ := cmd("run-due"); code != ExitOK || !strings.Contains(out, "Nothing is due.") {
		t.Fatalf("run-due (idle): %d %s", code, out)
	}

	// Pretend it last ran by schedule two hours ago: four missed times, one run.
	ctx := context.Background()
	backdate := func() {
		t.Helper()
		st, err := store.Open(ctx, datadir.DatabasePath(data), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		ago := time.Now().Add(-2 * time.Hour).UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
		if _, err := st.DB.ExecContext(ctx, `UPDATE flows SET schedule_anchor = ?`, ago); err != nil {
			t.Fatal(err)
		}
	}
	backdate()
	code, out, _ = cmd("run-due", "--json")
	var res []runOutput
	if code != ExitOK || json.Unmarshal([]byte(out), &res) != nil || len(res) != 1 || res[0].Run.Trigger != "run-due" || res[0].Run.Status != "succeeded" {
		t.Fatalf("run-due: %d %s", code, out)
	}
	if code, out, _ := cmd("run-due", "--json"); code != ExitOK || strings.TrimSpace(out) != "[]" {
		t.Fatalf("missed times must run once: %d %s", code, out)
	}

	// A due flow running elsewhere: exit 4, and its time is consumed.
	backdate()
	st, _ := store.Open(ctx, datadir.DatabasePath(data), nil)
	f, _ := st.GetFlow(ctx, "Notes to Team")
	if _, err := st.AcquireLock(ctx, f.ID, "elsewhere", "server", time.Minute); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if code, _, _ := cmd("run-due"); code != ExitLocked {
		t.Fatalf("locked: %d", code)
	}
	if code, out, _ := cmd("run-due"); code != ExitOK || !strings.Contains(out, "Nothing is due.") {
		t.Fatalf("after a skipped time: %d %s", code, out)
	}

	// Bad schedules in the import file are usage errors.
	if err := os.WriteFile(file, []byte(strings.Replace(doc, "every: 30", "every: 7", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := cmd("flow", "import", file); code != ExitUsage || !strings.Contains(errs, "schedule.every") {
		t.Fatalf("bad schedule: %d %s", code, errs)
	}
}
