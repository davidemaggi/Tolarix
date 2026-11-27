package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/davidemaggi/tolarix/core/internal/buildinfo"
)

func newVersionCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the Tolarix version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			info := buildinfo.Get()
			if asJSON {
				enc := json.NewEncoder(a.stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			fmt.Fprintf(a.stdout, "tolarix %s", info.Version)
			if info.Commit != "" {
				fmt.Fprintf(a.stdout, " (%s", info.Commit)
				if info.Date != "" {
					fmt.Fprintf(a.stdout, ", %s", info.Date)
				}
				fmt.Fprint(a.stdout, ")")
			}
			fmt.Fprintf(a.stdout, " %s %s\n", info.GoVersion, info.Platform)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}
