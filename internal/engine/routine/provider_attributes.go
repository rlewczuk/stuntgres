package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

// checkRoutineProviderAttributes admits actions, not differences: even a no-op
// execution clause would promise a runtime capability intrinsic providers lack.
// SQL/PL declarations own the shared execution environment, including their
// trigger and event-trigger entry points. Provider names are not authority.
func checkRoutineProviderAttributes(d *catalog.Procedure, attributes []parser.RoutineAttribute) error {
	if d.SQL != nil {
		return nil
	}
	for _, attribute := range attributes {
		switch attribute.Kind {
		case "security", "set", "resetall", "strict", "immutable", "stable", "volatile":
			return sqlError("0A000", "execution attributes are unsupported for native routine providers")
		}
	}
	return nil
}
