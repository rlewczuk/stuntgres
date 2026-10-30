package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/parser"
)

// checkRoutineProviderAttributes admits actions, not differences: even a no-op
// execution clause promises runtime support. SQL/PL and checked installed native
// trigger bodies own the shared environment; provider names grant no capability.
func checkRoutineProviderAttributes(d *catalog.Procedure, attributes []parser.RoutineAttribute) error {
	if d.RoutineDefinition() != nil {
		return nil
	}
	for _, attribute := range attributes {
		switch attribute.Kind {
		case "security", "set", "resetall":
			if extension.StockInsertUsername.SupportsDMLTrigger(d) {
				continue
			}
			return sqlError("0A000", "execution attributes are unsupported for native routine providers")
		case "immutable", "stable", "volatile":
			if d.HostConversion != nil { continue }
			return sqlError("0A000", "execution attributes are unsupported for native routine providers")
		case "strict":
			return sqlError("0A000", "execution attributes are unsupported for native routine providers")
		}
	}
	return nil
}
