package fdw

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Host is what WRAPPER, SERVER, USER MAPPING and IMPORT FOREIGN SCHEMA
// staging needs from the engine for one statement: the shared DDL host plus
// the stage's observed dependencies, object removal and foreign-table
// staging. Like ddl.Host, every method runs with Engine.mu held and never
// runs a provider callback.
type Host interface {
	ddl.Host

	// AddDomainBase records d among the stage's observed dependencies, so a
	// concurrent change to it rejects the publication.
	AddDomainBase(d catalog.Descriptor)
	// RemoveObject stages the removal of ref.
	RemoveObject(ref ids.ObjectRef, namespace bool) error
	// StageForeignTable stages CREATE or DROP FOREIGN TABLE and returns the
	// validator jobs and the dropped roots. It goes away when the
	// foreign-table decisions move here (26e).
	StageForeignTable(auth ddl.Authority, s *parser.ForeignDDL, input sqltype.InputEnvironment) ([]Validation, []ids.ObjectAddress, error)
}
