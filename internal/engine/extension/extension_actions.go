package extension

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

// Actions returns implementation's update actions, none unless it is an
// Updater.
func Actions(implementation catalog.ExtensionImplementation) []UpdateAction {
	if updater, ok := implementation.(Updater); ok {
		return updater.Updates()
	}
	return nil
}

// ValidateActions checks a registration: a valid version graph whose
// installable edges each have exactly one action, and no action off the graph.
func ValidateActions(implementation catalog.ExtensionImplementation) error {
	versions := implementation.Versions()
	if err := versions.Validate(implementation.Control().Version); err != nil {
		return err
	}
	seen := map[catalog.ExtensionVersionEdge]bool{}
	for _, action := range Actions(implementation) {
		if action.Stage == nil || seen[action.Edge] || !slices.Contains(versions.Edges, action.Edge) {
			return fmt.Errorf("stuntgres: invalid extension update action")
		}
		seen[action.Edge] = true
	}
	for _, edge := range versions.Edges {
		if versions.InstallPath(edge.From) != nil && !seen[edge] {
			return fmt.Errorf("stuntgres: executable extension edge has no action")
		}
	}
	return nil
}

// StagePath stages each edge of path after its first version, in order, and
// requires every action to leave ext at the edge's target version.
func StagePath(ctx context.Context, stage Stage, implementation catalog.ExtensionImplementation, ext *catalog.Extension, owner authorization.PrincipalRef, path []string) error {
	for i := 1; i < len(path); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		edge := catalog.ExtensionVersionEdge{From: path[i-1], To: path[i]}
		found := false
		for _, action := range Actions(implementation) {
			if action.Edge == edge {
				if err := action.Stage(stage, ext, owner); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if !found || ext.Version != edge.To {
			return fmt.Errorf("stuntgres: unavailable executable extension transition")
		}
	}
	return nil
}
