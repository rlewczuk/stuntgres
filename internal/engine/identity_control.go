package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func sequenceNumericSQL(options catalog.SequenceOptions) string {
	cycle := "NO CYCLE"
	if options.Cycle {
		cycle = "CYCLE"
	}
	return fmt.Sprintf("INCREMENT BY %d MINVALUE %d MAXVALUE %d START WITH %d CACHE %d %s", options.Increment, options.Min, options.Max, options.Start, options.Cache, cycle)
}

func identityDumpDivergent(options catalog.SequenceOptions, column sqltype.ID) bool {
	lo, hi, _ := sequenceTypeLimits(column)
	return options.Persistence != "logged" || options.SQLType != column || options.Min < lo || options.Max > hi || options.Start < lo || options.Start > hi
}

// Restore admits only the parameter-free configuration of an existing internal
// sequence. It is reconstruction in the isolated engine, not ALTER history.
func (e *Engine) validateRestoreIdentityConfiguration(op *Operation, syntax *parser.AlterSequence) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.resolveRelationReference(auth, syntax.Name, false)
	if err != nil {
		return err
	}
	relation, ok := auth.Catalog.Relation(ref.Address)
	if !ok || relation.Sequence == nil || !relation.Sequence.Identity {
		return sqlError("0A000", "restore accepts only generated sequence ownership or identity configuration")
	}
	if syntax.Action == "logged" || syntax.Action == "unlogged" {
		return nil
	}
	if syntax.Action != "" || len(syntax.Options) == 0 {
		return sqlError("0A000", "restore accepts only generated identity configuration")
	}
	for _, option := range syntax.Options {
		switch option.Name {
		case "as", "increment", "min", "max", "start", "cache", "cycle", "restart":
		default:
			return sqlError("0A000", "restore accepts only generated identity configuration")
		}
	}
	return nil
}
