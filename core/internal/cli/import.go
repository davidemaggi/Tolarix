package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/schedule"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

// importDoc is the YAML accepted by `tolarix flow import`.
type importDoc struct {
	Connections []struct {
		Name      string         `yaml:"name"`
		Kind      string         `yaml:"kind"`
		Config    map[string]any `yaml:"config"`
		Secret    string         `yaml:"secret"`    // discouraged: prefer secretEnv
		SecretEnv string         `yaml:"secretEnv"` // read the secret from this environment variable
	} `yaml:"connections"`
	Flows []struct {
		Name     string         `yaml:"name"`
		Enabled  *bool          `yaml:"enabled"`
		Master   string         `yaml:"master"`
		Scope    engine.Scope   `yaml:"scope"`
		Options  importOptions  `yaml:"options"`
		Timezone string         `yaml:"timezone"`
		Schedule map[string]any `yaml:"schedule"` // as in the API: { kind: daily, time: "07:30" }
		// BuiltInSchedule false leaves the schedule to "tolarix run-due".
		BuiltInSchedule *bool `yaml:"builtInSchedule"`
		Mirrors         []struct {
			Connection string         `yaml:"connection"`
			Location   map[string]any `yaml:"location"`
		} `yaml:"mirrors"`
	} `yaml:"flows"`
}

type importOptions struct {
	DeletePolicy  string `yaml:"deletePolicy"`
	Attachments   *bool  `yaml:"attachments"`
	Banner        bool   `yaml:"banner"`
	AdoptExisting bool   `yaml:"adoptExisting"`
}

const importExample = `connections:
  - name: My notes
    kind: obsidian
    config: { path: /home/me/vault }      # relative paths are relative to this file
  - name: Team vault
    kind: obsidian
    config: { path: /srv/team-vault }
  # - name: Team ADO
  #   kind: azuredevops-code
  #   config: { ... }
  #   secretEnv: TOLARIX_ADO_PAT          # never put secrets in the file

flows:
  - name: Notes to team
    master: My notes
    scope: { mode: all }                   # or: { mode: selected, include: [Projects] }
    mirrors:
      - connection: Team vault
        location: { folder: Mirror }
    options: { deletePolicy: archive, banner: true }
    schedule: { kind: weekly, days: [mon, tue, wed, thu, fri], time: "07:30" }
    # other kinds: { kind: manual } (default), { kind: interval, every: 30, unit: minutes },
    # { kind: daily, time: "07:30" }, { kind: monthly, day: 1, time: "06:00" }, { kind: cron, expr: "0 7 * * 1-5" }
    timezone: Europe/Rome                  # default: TOLARIX_TIMEZONE, TZ or UTC
    # builtInSchedule: false               # leave the schedule to "tolarix run-due" (cron)`

func newFlowImportCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "import <file.yaml>",
		Short: "Create or update connections and flows from a YAML file",
		Long: "Create or update connections and flows from a YAML file. Items are matched by name, so\n" +
			"importing the same file again updates them (mirrors keep their sync history).\n\nExample:\n\n" + importExample,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := os.ReadFile(args[0])
			if err != nil {
				return exitErr(ExitUsage, "read %s: %w", args[0], err)
			}
			var doc importDoc
			if err := yaml.Unmarshal(b, &doc); err != nil {
				return exitErr(ExitUsage, "%s is not valid YAML: %w", args[0], err)
			}
			rt, done, err := a.openRuntime(ctx)
			if err != nil {
				return err
			}
			defer done()
			base, _ := filepath.Abs(filepath.Dir(args[0]))
			return importAll(ctx, a, rt, &doc, base)
		},
	}
}

func importAll(ctx context.Context, a *app, rt *runtime, doc *importDoc, base string) error {
	ids := map[string]string{}
	for _, c := range doc.Connections {
		if c.Name == "" || c.Kind == "" {
			return exitErr(ExitUsage, "every connection needs a name and a kind")
		}
		spec, ok := connectors.Lookup(connectors.Kind(c.Kind))
		if !ok {
			return exitErr(ExitUsage, "connection %q: unknown kind %q", c.Name, c.Kind)
		}
		if p, ok := c.Config["path"].(string); ok && spec.Kind == "obsidian" && !filepath.IsAbs(p) {
			c.Config["path"] = filepath.Join(base, p)
		}
		cfg, err := json.Marshal(c.Config)
		if err != nil {
			return err
		}
		var sec *secrets.String
		switch {
		case c.SecretEnv != "":
			v := os.Getenv(c.SecretEnv)
			if v == "" {
				return exitErr(ExitUsage, "connection %q: environment variable %s is empty", c.Name, c.SecretEnv)
			}
			s := secrets.New(v)
			sec = &s
		case c.Secret != "":
			s := secrets.New(c.Secret)
			sec = &s
		}
		conn := store.Connection{Name: c.Name, Kind: c.Kind, Config: cfg}
		if existing, err := rt.store.GetConnection(ctx, c.Name); err == nil {
			conn.ID = existing.ID
		}
		if _, err := connectors.Build(connectors.Kind(c.Kind), cfg, secretOrEmpty(sec)); err != nil {
			return exitErr(ExitUsage, "connection %q: %w", c.Name, err)
		}
		saved, err := rt.store.SaveConnection(ctx, conn, sec, rt.cipher)
		if err != nil {
			return err
		}
		ids[c.Name] = saved.ID
		verb := "created"
		if conn.ID != "" {
			verb = "updated"
		}
		fmt.Fprintf(a.stdout, "connection %q %s\n", c.Name, verb)
	}

	resolve := func(name string) (string, error) {
		if id, ok := ids[name]; ok {
			return id, nil
		}
		c, err := rt.store.GetConnection(ctx, name)
		if err != nil {
			return "", exitErr(ExitUsage, "no connection named %q", name)
		}
		return c.ID, nil
	}

	for _, f := range doc.Flows {
		if f.Name == "" || f.Master == "" || len(f.Mirrors) == 0 {
			return exitErr(ExitUsage, "every flow needs a name, a master and at least one mirror")
		}
		srcID, err := resolve(f.Master)
		if err != nil {
			return err
		}
		flow := store.Flow{Name: f.Name, Enabled: f.Enabled == nil || *f.Enabled, SourceConnectionID: srcID,
			Scope: f.Scope, Timezone: f.Timezone, ScheduleEnabled: true,
			Options: engine.Options{
				DeletePolicy: engine.DeletePolicy(f.Options.DeletePolicy), Attachments: f.Options.Attachments,
				Banner: f.Options.Banner, AdoptExisting: f.Options.AdoptExisting,
			}}
		var existingTargets []store.FlowTarget
		if existing, err := rt.store.GetFlow(ctx, f.Name); err == nil {
			flow.ID, flow.Schedule, flow.ScheduleEnabled = existing.ID, existing.Schedule, existing.ScheduleEnabled
			if f.Timezone == "" {
				flow.Timezone = existing.Timezone
			}
			existingTargets = existing.Targets
		}
		if flow.Timezone == "" {
			flow.Timezone = instanceTimezone()
		}
		if flow.Timezone != "" {
			if _, err := time.LoadLocation(flow.Timezone); err != nil {
				return exitErr(ExitUsage, "flow %q: unknown timezone %q", f.Name, flow.Timezone)
			}
		}
		if f.Schedule != nil {
			raw, _ := json.Marshal(f.Schedule)
			sc, err := schedule.Parse(raw)
			if err != nil {
				return exitErr(ExitUsage, "flow %q: %w", f.Name, err)
			}
			flow.Schedule = sc.JSON()
		}
		if f.BuiltInSchedule != nil {
			flow.ScheduleEnabled = *f.BuiltInSchedule
		}
		used := map[string]bool{}
		for _, m := range f.Mirrors {
			cid, err := resolve(m.Connection)
			if err != nil {
				return err
			}
			loc, _ := json.Marshal(m.Location)
			t := store.FlowTarget{ConnectionID: cid, Location: loc}
			// Keep the target id (and its mapping history) when the mirror is unchanged.
			for _, et := range existingTargets {
				if !used[et.ID] && et.ConnectionID == cid && jsonEqual(et.Location, loc) {
					t.ID = et.ID
					used[et.ID] = true
					break
				}
			}
			flow.Targets = append(flow.Targets, t)
		}
		verb := "created"
		if flow.ID != "" {
			verb = "updated"
		}
		if _, err := rt.store.SaveFlow(ctx, flow); err != nil {
			return exitErr(ExitFailure, "flow %q: %w", f.Name, err)
		}
		fmt.Fprintf(a.stdout, "flow %q %s\n", f.Name, verb)
	}
	return nil
}

func secretOrEmpty(s *secrets.String) secrets.String {
	if s == nil {
		return secrets.String{}
	}
	return *s
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return string(a) == string(b)
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}
