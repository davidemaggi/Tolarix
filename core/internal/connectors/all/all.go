// Package all registers every built-in connector. Import it for side effects.
package all

import (
	_ "github.com/davidemaggi/tolarix/core/internal/connectors/azuredevops" // Azure DevOps code wikis
	_ "github.com/davidemaggi/tolarix/core/internal/connectors/confluence"  // Confluence Data Center / Server
	_ "github.com/davidemaggi/tolarix/core/internal/connectors/obsidian"    // Obsidian vaults
)
