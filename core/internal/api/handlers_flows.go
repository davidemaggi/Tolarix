package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/davidemaggi/tolarix/core/internal/api/oapi"
	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

const maxBannerText = 2000

// flowState is the live data added to flows in responses.
type flowState struct {
	latest map[string]store.Run
	locks  map[string]store.LockInfo
}

func (s *server) flowState(ctx context.Context) (flowState, error) {
	latest, err := s.b.Store.LatestRuns(ctx)
	if err != nil {
		return flowState{}, err
	}
	locks, err := s.b.Store.ActiveLocks(ctx)
	if err != nil {
		return flowState{}, err
	}
	return flowState{latest: latest, locks: locks}, nil
}

func (s *server) flowOut(f store.Flow, st flowState) oapi.Flow {
	out := oapi.Flow{
		Id: f.ID, Name: f.Name, Enabled: f.Enabled, SourceConnectionId: f.SourceConnectionID,
		Scope:    scopeOut(f.Scope),
		Options:  optionsOut(f.Options),
		Schedule: rawOrDefault(f.Schedule, `{"kind":"manual"}`), ScheduleEnabled: f.ScheduleEnabled,
		Timezone: f.Timezone, Targets: make([]oapi.Mirror, 0, len(f.Targets)),
		CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt,
	}
	for _, t := range f.Targets {
		out.Targets = append(out.Targets, oapi.Mirror{Id: t.ID, ConnectionId: t.ConnectionID, Location: rawOrEmptyObject(t.Location)})
	}
	if r, ok := st.latest[f.ID]; ok {
		ro := runOut(r)
		out.LastRun = &ro
	}
	if li, ok := st.locks[f.ID]; ok && s.b.Runner.LockAlive(li, time.Now()) {
		out.Running = &oapi.RunningInfo{RunId: li.RunID, StartedAt: li.AcquiredAt, Holder: li.Holder}
	}
	return out
}

func scopeOut(sc engine.Scope) oapi.Scope {
	out := oapi.Scope{Mode: oapi.ScopeMode(sc.Mode)}
	if out.Mode == "" {
		out.Mode = oapi.ScopeModeAll
	}
	if len(sc.Include) > 0 {
		out.Include = ptr(sc.Include)
	}
	if len(sc.Exclude) > 0 {
		out.Exclude = ptr(sc.Exclude)
	}
	return out
}

func optionsOut(o engine.Options) oapi.FlowOptions {
	out := oapi.FlowOptions{
		DeletePolicy:  ptr(oapi.FlowOptionsDeletePolicy(o.Policy())),
		Attachments:   ptr(o.AttachmentsEnabled()),
		Banner:        ptr(o.Banner),
		AdoptExisting: ptr(o.AdoptExisting),
	}
	if o.BannerText != "" {
		out.BannerText = ptr(o.BannerText)
	}
	return out
}

func (s *server) ListFlows(w http.ResponseWriter, r *http.Request) {
	flows, err := s.b.Store.ListFlows(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	st, err := s.flowState(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]oapi.Flow, 0, len(flows))
	for _, f := range flows {
		out = append(out, s.flowOut(f, st))
	}
	writeJSON(w, http.StatusOK, out)
}

// flow looks a flow up by id only.
func (s *server) flow(ctx context.Context, id string) (store.Flow, error) {
	f, err := s.b.Store.GetFlow(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && f.ID != id) {
		return store.Flow{}, errNotFound("flow")
	}
	return f, err
}

func (s *server) GetFlow(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	f, err := s.flow(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeFlow(w, r, http.StatusOK, f)
}

func (s *server) writeFlow(w http.ResponseWriter, r *http.Request, status int, f store.Flow) {
	st, err := s.flowState(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, status, s.flowOut(f, st))
}

func (s *server) CreateFlow(w http.ResponseWriter, r *http.Request) { s.saveFlow(w, r, "") }

func (s *server) UpdateFlow(w http.ResponseWriter, r *http.Request, id oapi.Id) { s.saveFlow(w, r, id) }

func (s *server) saveFlow(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	var in oapi.FlowInput
	if err := decodeJSON(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var existing *store.Flow
	if id != "" {
		f, err := s.flow(ctx, id)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		if err := s.notRunning(ctx, f, "changed"); err != nil {
			s.writeErr(w, r, err)
			return
		}
		existing = &f
	}
	f, err := s.validateFlow(ctx, in, existing)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	saved, err := s.b.Store.SaveFlow(ctx, f)
	if errors.Is(err, store.ErrConflict) {
		s.writeErr(w, r, &apiError{Status: http.StatusConflict, Code: "name_taken",
			Message: "Another flow already has this name.", Fields: map[string]string{"name": "This name is already used."}})
		return
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
		s.log.Info("flow created", "flow", saved.Name)
	} else {
		s.log.Info("flow updated", "flow", saved.Name)
	}
	s.writeFlow(w, r, status, saved)
}

// notRunning refuses to change or delete a flow during a run: targets and
// mappings in use by the run must not disappear under it.
func (s *server) notRunning(ctx context.Context, f store.Flow, verb string) error {
	li, err := s.b.Store.LockHolder(ctx, f.ID)
	if err != nil {
		return err
	}
	if li != nil && s.b.Runner.LockAlive(*li, time.Now()) {
		return errConflict("running", fmt.Sprintf("This flow is running. It can be %s when the run ends.", verb))
	}
	return nil
}

// validateFlow turns the input into a flow to save, keeping the values the
// input omits from existing (nil when creating).
func (s *server) validateFlow(ctx context.Context, in oapi.FlowInput, existing *store.Flow) (store.Flow, error) {
	fields := map[string]string{}
	f := store.Flow{Enabled: true, ScheduleEnabled: true, Timezone: s.b.Timezone}
	if existing != nil {
		f.ID, f.Enabled, f.ScheduleEnabled, f.Timezone, f.Schedule =
			existing.ID, existing.Enabled, existing.ScheduleEnabled, existing.Timezone, existing.Schedule
	}
	if f.Timezone == "" {
		f.Timezone = "UTC"
	}

	f.Name = strings.TrimSpace(in.Name)
	switch {
	case f.Name == "":
		fields["name"] = "Enter a name."
	case utf8.RuneCountInString(f.Name) > maxNameLen:
		fields["name"] = fmt.Sprintf("Use at most %d characters.", maxNameLen)
	}
	if in.Enabled != nil {
		f.Enabled = *in.Enabled
	}
	if in.ScheduleEnabled != nil {
		f.ScheduleEnabled = *in.ScheduleEnabled
	}
	if in.Timezone != nil && strings.TrimSpace(*in.Timezone) != "" {
		tz := strings.TrimSpace(*in.Timezone)
		if _, err := time.LoadLocation(tz); err != nil {
			fields["timezone"] = "Unknown timezone."
		}
		f.Timezone = tz
	}
	if in.Schedule != nil {
		// Schedules are interpreted from M6; until then only the shape is checked.
		var obj map[string]any
		if err := json.Unmarshal(*in.Schedule, &obj); err != nil || obj == nil {
			fields["schedule"] = "The schedule must be a JSON object."
		} else {
			f.Schedule = *in.Schedule
		}
	}

	conns, err := s.b.Store.ListConnections(ctx)
	if err != nil {
		return f, err
	}
	byID := map[string]store.Connection{}
	for _, c := range conns {
		byID[c.ID] = c
	}

	f.SourceConnectionID = in.SourceConnectionId
	if src, ok := byID[in.SourceConnectionId]; !ok {
		fields["sourceConnectionId"] = "Choose the Master wiki."
	} else if sp, ok := connectors.Lookup(connectors.Kind(src.Kind)); !ok || !sp.CanRead {
		fields["sourceConnectionId"] = "This wiki type cannot be a Master."
	}

	f.Scope = engine.Scope{Mode: string(in.Scope.Mode)}
	if in.Scope.Include != nil {
		f.Scope.Include = cleanList(*in.Scope.Include)
	}
	if in.Scope.Exclude != nil {
		f.Scope.Exclude = cleanList(*in.Scope.Exclude)
	}
	switch in.Scope.Mode {
	case oapi.ScopeModeAll:
		f.Scope.Include = nil
	case oapi.ScopeModeSelected:
		if len(f.Scope.Include) == 0 {
			fields["scope.include"] = "Choose at least one page or folder."
		}
	default:
		fields["scope.mode"] = "Choose what to mirror."
	}

	opts := in.Options
	if opts.DeletePolicy != nil {
		if !opts.DeletePolicy.Valid() {
			fields["options.deletePolicy"] = "Choose what happens to pages removed from the Master."
		}
		f.Options.DeletePolicy = engine.DeletePolicy(*opts.DeletePolicy)
	}
	f.Options.Attachments = opts.Attachments
	if opts.Banner != nil {
		f.Options.Banner = *opts.Banner
	}
	if opts.AdoptExisting != nil {
		f.Options.AdoptExisting = *opts.AdoptExisting
	}
	if opts.BannerText != nil {
		f.Options.BannerText = strings.TrimSpace(*opts.BannerText)
		if utf8.RuneCountInString(f.Options.BannerText) > maxBannerText {
			fields["options.bannerText"] = fmt.Sprintf("Use at most %d characters.", maxBannerText)
		}
	}

	if len(in.Targets) == 0 {
		fields["targets"] = "Add at least one Mirror."
	}
	ownTargets := map[string]bool{}
	if existing != nil {
		for _, t := range existing.Targets {
			ownTargets[t.ID] = true
		}
	}
	seen := map[string]int{}
	usedIDs := map[string]bool{}
	for i, t := range in.Targets {
		key := fmt.Sprintf("targets.%d", i)
		ft := store.FlowTarget{ConnectionID: t.ConnectionId}
		if t.Id != nil && *t.Id != "" {
			if !ownTargets[*t.Id] || usedIDs[*t.Id] {
				fields[key] = "This Mirror does not belong to the flow. Reload the page and try again."
			}
			ft.ID = *t.Id
			usedIDs[*t.Id] = true
		}
		c, ok := byID[t.ConnectionId]
		if !ok {
			fields[key+".connectionId"] = "Choose a wiki."
			f.Targets = append(f.Targets, ft)
			continue
		}
		sp, ok := connectors.Lookup(connectors.Kind(c.Kind))
		if !ok || !sp.CanWrite {
			fields[key+".connectionId"] = "This wiki type cannot be a Mirror."
		}
		if t.ConnectionId == in.SourceConnectionId {
			fields[key+".connectionId"] = "A Mirror cannot be the Master wiki itself."
		}
		loc, locErrs := validateLocation(sp, t.Location)
		for k, v := range locErrs {
			fields[key+".location."+k] = v
		}
		ft.Location = loc
		sig := t.ConnectionId + "|" + string(loc)
		if j, dup := seen[sig]; dup {
			fields[key] = fmt.Sprintf("This is the same as Mirror %d.", j+1)
		}
		seen[sig] = i
		f.Targets = append(f.Targets, ft)
	}

	if len(fields) > 0 {
		return f, errValidation(fields)
	}
	return f, nil
}

// validateLocation normalises a target location object and checks its
// required fields.
func validateLocation(sp connectors.Spec, raw json.RawMessage) (json.RawMessage, map[string]string) {
	errs := map[string]string{}
	obj := map[string]any{}
	if b := bytes.TrimSpace(raw); len(b) > 0 && string(b) != "null" {
		if err := json.Unmarshal(b, &obj); err != nil {
			errs["_"] = "The location must be a JSON object."
			return json.RawMessage("{}"), errs
		}
	}
	for _, fl := range sp.LocationFields {
		v, present := obj[fl.Name]
		if str, isStr := v.(string); isStr {
			v = strings.TrimSpace(str)
			obj[fl.Name] = v
		}
		if fl.Required && (!present || v == nil || v == "") {
			errs[fl.Name] = "This field is required."
		}
	}
	b, _ := json.Marshal(obj) // keys sorted: equal locations compare equal
	return b, errs
}

func cleanList(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (s *server) DeleteFlow(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	ctx := r.Context()
	f, err := s.flow(ctx, id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.notRunning(ctx, f, "deleted"); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.b.Store.DeleteFlow(ctx, f.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = errNotFound("flow")
		}
		s.writeErr(w, r, err)
		return
	}
	s.log.Info("flow deleted", "flow", f.Name)
	w.WriteHeader(http.StatusNoContent)
}

func rawOrDefault(b json.RawMessage, def string) json.RawMessage {
	if len(bytes.TrimSpace(b)) == 0 || string(b) == "null" {
		return json.RawMessage(def)
	}
	return b
}
