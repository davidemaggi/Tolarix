package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newConnectionCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "connection",
		Aliases: []string{"connections", "conn"},
		Short:   "List and test wiki connections",
	}
	cmd.AddCommand(newConnectionListCommand(a), newConnectionTestCommand(a))
	return cmd
}

func newConnectionListCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List connections",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, done, err := a.openRuntime(cmd.Context())
			if err != nil {
				return err
			}
			defer done()
			conns, err := rt.store.ListConnections(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				if conns == nil {
					return printJSON(a.stdout, []any{})
				}
				return printJSON(a.stdout, conns)
			}
			if len(conns) == 0 {
				fmt.Fprintln(a.stdout, "No connections yet.")
				return nil
			}
			tw := table(a.stdout)
			fmt.Fprintln(tw, "NAME\tTYPE\tSECRET\tID")
			for _, c := range conns {
				sec := "-"
				if c.HasSecret {
					sec = "set"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Name, c.Kind, sec, shortID(c.ID))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}

func newConnectionTestCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "test <name-or-id>",
		Short: "Check that Tolarix can reach a wiki with the saved settings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			rt, done, err := a.openRuntime(ctx)
			if err != nil {
				return err
			}
			defer done()
			conn, err := rt.store.GetConnection(ctx, args[0])
			if err != nil {
				return notFoundErr("connection", args[0], err)
			}
			c, err := rt.runner.Build(ctx, conn)
			if err != nil {
				return exitErr(ExitFailure, "connection %q is not configured correctly: %w", conn.Name, err)
			}
			if err := c.Test(ctx); err != nil {
				return exitErr(ExitFailure, "connection %q failed: %w", conn.Name, err)
			}
			fmt.Fprintf(a.stdout, "Connection %q works.\n", conn.Name)
			return nil
		},
	}
}
