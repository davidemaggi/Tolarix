package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	_ "github.com/davidemaggi/tolarix/core/internal/connectors/all" // register connectors
	"github.com/davidemaggi/tolarix/core/internal/runner"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

// runtime is what commands that touch flows need.
type runtime struct {
	store  *store.Store
	cipher *secrets.Cipher
	runner *runner.Runner
}

func (a *app) openRuntime(ctx context.Context) (*runtime, func(), error) {
	st, dir, err := a.openStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	key, _, err := secrets.LoadKey(dir)
	if err != nil {
		_ = st.Close()
		return nil, nil, &ExitError{Code: ExitConfigError, Err: err}
	}
	ci, err := secrets.NewCipher(key)
	if err != nil {
		_ = st.Close()
		return nil, nil, &ExitError{Code: ExitConfigError, Err: err}
	}
	rt := &runtime{store: st, cipher: ci, runner: &runner.Runner{Store: st, Cipher: ci, Log: a.log}}
	return rt, func() { _ = st.Close() }, nil
}

func notFoundErr(what, name string, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return exitErr(ExitNotFound, "no %s named %q (list them with: tolarix %s list)", what, name, what)
	}
	return err
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func table(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 2, 2, ' ', 0) }

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// shortID shows the last 8 characters: ids are UUIDv7, whose first characters
// are a timestamp and so are shared by ids created close together.
func shortID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
