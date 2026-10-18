package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// JSONReturningPseudoType reports the pseudo-types that SQL/JSON RETURNING
// clauses reject.
func JSONReturningPseudoType(d *sqltype.Descriptor) bool {
	if d == nil || !d.IsBuiltin() {
		return false
	}
	switch d.Name() {
	case "record", "cstring", "any", "anyarray", "anyelement", "anynonarray", "anyenum", "anyrange", "anymultirange", "anycompatible", "anycompatiblearray", "anycompatiblenonarray", "anycompatiblerange", "anycompatiblemultirange", "void", "trigger", "event_trigger", "language_handler", "internal", "fdw_handler", "index_am_handler", "table_am_handler", "tsm_handler", "pg_ddl_command":
		return true
	}
	return false
}

// Encoding requires the resolved BYTEA identity, not a domain over BYTEA.
// Owners call this before output planning, preserving their input error order.
func ValidateJSONOutputFormat(returning sqltype.Usage, format *parser.JSONFormat) error {
	if format == nil || format.Encoding == "" {
		return nil
	}
	if returning.Type == nil || returning.Type.ID() != sqltype.Bytea {
		return expr.ErrorAt("0A000", "cannot set JSON encoding for non-bytea output types", format.Pos)
	}
	if format.Encoding != "UTF8" {
		d := expr.ErrorAt("0A000", "unsupported JSON encoding", format.Pos).(*sqlstate.Error)
		d.Hint = "Only UTF8 JSON encoding is supported."
		return d
	}
	return nil
}

// JSONReturningCoercion plans a SQL/JSON RETURNING conversion, positioning
// its error at the output FORMAT clause when one is written.
func JSONReturningCoercion(source, target sqltype.Usage, format *parser.JSONFormat) (sqltype.CoercionPlan, error) {
	plan, err := sqltype.PlanJSONReturning(source, target)
	if d, ok := err.(*sqlstate.Error); ok && format != nil {
		copy := *d
		copy.Position = fmt.Sprint(format.Pos.Character)
		err = &copy
	}
	return plan, err
}
