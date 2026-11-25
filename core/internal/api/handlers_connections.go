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
	"github.com/davidemaggi/tolarix/core/internal/secrets"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

// testTimeout bounds connection tests and tree browsing.
const testTimeout = 30 * time.Second

const maxNameLen = 100

func (s *server) ListConnectorKinds(w http.ResponseWriter, _ *http.Request) {
	out := []oapi.ConnectorKind{}
	for _, sp := range connectors.All() {
		out = append(out, oapi.ConnectorKind{
			Kind: string(sp.Kind), DisplayName: sp.DisplayName, CanRead: sp.CanRead, CanWrite: sp.CanWrite,
			Fields: fieldsOut(sp.Fields), LocationFields: fieldsOut(sp.LocationFields),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func fieldsOut(fs []connectors.Field) []oapi.ConnectorField {
	out := make([]oapi.ConnectorField, 0, len(fs))
	for _, f := range fs {
		cf := oapi.ConnectorField{Name: f.Name, Type: oapi.ConnectorFieldType(f.Type), Required: f.Required}
		if f.Secret {
			cf.Secret = ptr(true)
		}
		out = append(out, cf)
	}
	return out
}

// usage maps connection ids to the flows that use them (as Master or Mirror).
func (s *server) usage(ctx context.Context) (map[string][]oapi.FlowRef, error) {
	flows, err := s.b.Store.ListFlows(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]oapi.FlowRef{}
	add := func(connID string, f store.Flow) {
		for _, ref := range out[connID] {
			if ref.Id == f.ID {
				return
			}
		}
		out[connID] = append(out[connID], oapi.FlowRef{Id: f.ID, Name: f.Name})
	}
	for _, f := range flows {
		add(f.SourceConnectionID, f)
		for _, t := range f.Targets {
			add(t.ConnectionID, f)
		}
	}
	return out, nil
}

func connectionOut(c store.Connection, used []oapi.FlowRef) oapi.Connection {
	if used == nil {
		used = []oapi.FlowRef{}
	}
	return oapi.Connection{
		Id: c.ID, Name: c.Name, Kind: c.Kind, Config: rawOrEmptyObject(c.Config), HasSecret: c.HasSecret,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, UsedBy: used,
	}
}

func (s *server) ListConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := s.b.Store.ListConnections(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	used, err := s.usage(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]oapi.Connection, 0, len(conns))
	for _, c := range conns {
		out = append(out, connectionOut(c, used[c.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) GetConnection(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	c, err := s.connection(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	used, err := s.usage(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, connectionOut(c, used[c.ID]))
}

// connection looks a connection up by id only (names are for the CLI).
func (s *server) connection(ctx context.Context, id string) (store.Connection, error) {
	c, err := s.b.Store.GetConnection(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && c.ID != id) {
		return store.Connection{}, errNotFound("connection")
	}
	return c, err
}

func (s *server) CreateConnection(w http.ResponseWriter, r *http.Request) {
	s.saveConnection(w, r, "")
}

func (s *server) UpdateConnection(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	s.saveConnection(w, r, id)
}

func (s *server) saveConnection(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	var in oapi.ConnectionInput
	if err := decodeJSON(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var existing store.Connection
	if id != "" {
		var err error
		if existing, err = s.connection(ctx, id); err != nil {
			s.writeErr(w, r, err)
			return
		}
		if in.Kind != existing.Kind {
			s.writeErr(w, r, errValidation(map[string]string{
				"kind": "The wiki type of a saved connection cannot change. Create a new connection instead.",
			}))
			return
		}
	}
	name, cfg, err := validateConnection(in.Name, in.Kind, in.Config)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var sec *secrets.String
	if in.Secret != nil {
		v := secrets.New(*in.Secret)
		sec = &v
	}
	// Check that the connector accepts the settings (it does not connect).
	effective, err := s.effectiveSecret(ctx, id, sec)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if _, err := connectors.Build(connectors.Kind(in.Kind), cfg, effective); err != nil {
		s.writeErr(w, r, errValidation(map[string]string{"config": capitalize(err.Error())}))
		return
	}
	saved, err := s.b.Store.SaveConnection(ctx, store.Connection{ID: id, Name: name, Kind: in.Kind, Config: cfg}, sec, s.b.Cipher)
	if errors.Is(err, store.ErrConflict) {
		s.writeErr(w, r, &apiError{Status: http.StatusConflict, Code: "name_taken",
			Message: "Another connection already has this name.", Fields: map[string]string{"name": "This name is already used."}})
		return
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	used, err := s.usage(ctx)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
		s.log.Info("connection created", "connection", saved.Name, "kind", saved.Kind)
	} else {
		s.log.Info("connection updated", "connection", saved.Name)
	}
	writeJSON(w, status, connectionOut(saved, used[saved.ID]))
}

// effectiveSecret is the secret a connection will use: the given one, or
// the stored one when sec is nil and the connection exists.
func (s *server) effectiveSecret(ctx context.Context, id string, sec *secrets.String) (secrets.String, error) {
	if sec != nil {
		return *sec, nil
	}
	if id == "" {
		return secrets.String{}, nil
	}
	return s.b.Store.ConnectionSecret(ctx, id, s.b.Cipher)
}

// validateConnection checks the parts every connector shares: a name, a
// known kind, a JSON object config with the required fields filled in.
func validateConnection(name, kind string, cfg json.RawMessage) (string, json.RawMessage, error) {
	fields := map[string]string{}
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		fields["name"] = "Enter a name."
	case utf8.RuneCountInString(name) > maxNameLen:
		fields["name"] = fmt.Sprintf("Use at most %d characters.", maxNameLen)
	}
	spec, ok := connectors.Lookup(connectors.Kind(kind))
	if !ok {
		fields["kind"] = "Choose a wiki type."
		return name, nil, errValidation(fields)
	}
	obj := map[string]any{}
	if len(bytes.TrimSpace(cfg)) > 0 && string(bytes.TrimSpace(cfg)) != "null" {
		if err := json.Unmarshal(cfg, &obj); err != nil {
			fields["config"] = "The settings must be a JSON object."
			return name, nil, errValidation(fields)
		}
	}
	for _, f := range spec.Fields {
		if f.Secret {
			delete(obj, f.Name) // secrets travel in "secret", never in config
			continue
		}
		v, present := obj[f.Name]
		if str, isStr := v.(string); isStr {
			v = strings.TrimSpace(str)
			obj[f.Name] = v
		}
		if f.Required && (!present || v == nil || v == "") {
			fields["config."+f.Name] = "This field is required."
		}
	}
	if len(fields) > 0 {
		return name, nil, errValidation(fields)
	}
	out, err := json.Marshal(obj)
	return name, out, err
}

func (s *server) DeleteConnection(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	ctx := r.Context()
	c, err := s.connection(ctx, id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	used, err := s.usage(ctx)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if refs := used[c.ID]; len(refs) > 0 {
		names := make([]string, len(refs))
		for i, f := range refs {
			names[i] = f.Name
		}
		s.writeErr(w, r, errConflict("in_use",
			"This connection is used by: "+strings.Join(names, ", ")+". Remove it from those flows first."))
		return
	}
	if err := s.b.Store.DeleteConnection(ctx, c.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = errNotFound("connection")
		}
		s.writeErr(w, r, err)
		return
	}
	s.log.Info("connection deleted", "connection", c.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) TestConnection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in oapi.ConnectionTestRequest
	if err := decodeJSON(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	// The name is not part of a test; validate the rest with a placeholder.
	_, cfg, err := validateConnection("test", in.Kind, in.Config)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	id := ""
	if in.ConnectionId != nil && *in.ConnectionId != "" {
		c, err := s.connection(ctx, *in.ConnectionId)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		if c.Kind != in.Kind {
			s.writeErr(w, r, errValidation(map[string]string{"kind": "The wiki type does not match the saved connection."}))
			return
		}
		id = c.ID
	}
	var sec *secrets.String
	if in.Secret != nil {
		v := secrets.New(*in.Secret)
		sec = &v
	}
	effective, err := s.effectiveSecret(ctx, id, sec)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	conn, err := connectors.Build(connectors.Kind(in.Kind), cfg, effective)
	if err != nil {
		writeJSON(w, http.StatusOK, oapi.TestResult{Ok: false, Message: capitalize(err.Error())})
		return
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	if err := conn.Test(tctx); err != nil {
		msg := capitalize(err.Error())
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "The wiki did not answer within 30 seconds. Check the address and your network."
		}
		writeJSON(w, http.StatusOK, oapi.TestResult{Ok: false, Message: msg})
		return
	}
	writeJSON(w, http.StatusOK, oapi.TestResult{Ok: true, Message: "Tolarix can reach this wiki."})
}

func (s *server) BrowseConnection(w http.ResponseWriter, r *http.Request, id oapi.Id, params oapi.BrowseConnectionParams) {
	ctx := r.Context()
	c, err := s.connection(ctx, id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	built, err := s.b.Runner.Build(ctx, c)
	if err != nil {
		s.writeErr(w, r, errBadRequest("browse_failed", "%s", capitalize(err.Error())))
		return
	}
	src, ok := built.(connectors.Source)
	if !ok {
		s.writeErr(w, r, errBadRequest("not_a_source", "This wiki type cannot be a Master."))
		return
	}
	parent := ""
	if params.Parent != nil {
		parent = *params.Parent
	}
	bctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	nodes, err := src.Browse(bctx, parent)
	if err != nil {
		s.writeErr(w, r, errBadRequest("browse_failed", "%s", capitalize(err.Error())))
		return
	}
	out := make([]oapi.BrowseNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, oapi.BrowseNode{Id: n.ID, Title: n.Title, HasChildren: n.HasChildren})
	}
	writeJSON(w, http.StatusOK, out)
}

func rawOrEmptyObject(b json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(b)) == 0 || string(b) == "null" {
		return json.RawMessage("{}")
	}
	return b
}

func ptr[T any](v T) *T { return &v }

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return strings.ToUpper(string(r)) + s[n:]
}
