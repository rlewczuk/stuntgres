package fdw

import (
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Host is what SQL/MED staging needs from the engine for one statement:
// the table host (and through it the shared DDL host and the stage's observed
// dependencies), so CREATE FOREIGN TABLE stages through
// table.StageCreateTable, plus object removal and utility relation
// resolution. Like ddl.Host, every method runs with Engine.mu held, never
// waits and never runs a provider callback.
type Host interface {
	table.Host

	// RemoveObject stages the removal of ref.
	RemoveObject(ref ids.ObjectRef, namespace bool) error
	// ResolveRelation resolves a relation named by a utility statement:
	// a missing schema is 3F000 and no diagnostic carries a position.
	ResolveRelation(auth ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error)
}
