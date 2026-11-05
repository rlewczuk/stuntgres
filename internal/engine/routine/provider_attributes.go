package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/parser"
)

// checkRoutineProviderAttributes admits actions, not differences: even a no-op
// execution clause promises runtime support. SQL/PL and checked installed native
// trigger/connection bodies own the shared environment; names grant no capability.
func checkRoutineProviderAttributes(d *catalog.Procedure, attributes []parser.RoutineAttribute) error {
	if d.RoutineDefinition() != nil {
		return nil
	}
	for _, attribute := range attributes {
		switch attribute.Kind {
		case "set", "resetall":
			if extension.StockInsertUsername.SupportsDMLTrigger(d) || extension.StockDblink.SupportsConnectionRoutine(d) || extension.StockXML2.SupportsRoutineConfiguration(d) {
				continue
			}
			return sqlError("0A000", "execution attributes are unsupported for native routine providers")
		case "security":
			if extension.StockInsertUsername.SupportsDMLTrigger(d) || extension.StockDblink.SupportsConnectionRoutine(d) {
				continue
			}
			return sqlError("0A000", "execution attributes are unsupported for native routine providers")
		case "immutable", "stable", "volatile":
			if d.HostConversion != nil {
				continue
			}
			return sqlError("0A000", "execution attributes are unsupported for native routine providers")
		case "strict":
			return sqlError("0A000", "execution attributes are unsupported for native routine providers")
		}
	}
	return nil
}
