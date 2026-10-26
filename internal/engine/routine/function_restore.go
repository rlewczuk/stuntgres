package routine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// RestoreDirective defers catalog-dependent quoted SQL body analysis to
// invocation during private replay; dumps emit it before their SQL routines.
const RestoreDirective = "SET check_function_bodies = false;"

// ReplayDirective reports whether stmt is exactly RestoreDirective in data.
// Match the exact pg_dump-compatible directive, not arbitrary SET expressions.
// This flag exists on Restore's private operation only, never live settings.
func ReplayDirective(data string, stmt parser.Statement) bool {
	s, ok := stmt.(*parser.Set)
	if !ok || s.Local || s.Default || s.Name.Text != "check_function_bodies" || len(s.Values) != 1 || s.Values[0] != "false" {
		return false
	}
	start := s.Pos.Offset
	return start >= 0 && len(data)-start >= len(RestoreDirective) && data[start:start+len(RestoreDirective)] == RestoreDirective
}

// RebindSQLDefinitions rebinds the SQL routines of state to the mapped types.
// Typed SQL definitions are recreated against the destination type generation;
// syntax is immutable and text-body names remain runtime analysis, not edges.
// Complete formal, result and PL-local Usages belong to the shared definition
// mapper, so current declarations and historical captures share one payload.
func RebindSQLDefinitions(state *catalog.State, remap *catalog.RoutineRemap) error {
	for address, descriptor := range state.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || d.SQL == nil && d.RangeConstructor == nil && len(d.Body) == 0 {
			continue
		}
		mapped, err := remap.Procedure(d)
		if err != nil {
			return err
		}
		if mapped.SQL == nil {
			state.SetDescriptor(address, mapped)
			continue
		}
		definition, err := remap.Definition(mapped.SQL)
		if err != nil {
			return err
		}
		next := *mapped
		next.SQL = definition
		next.Result = ids.OID(definition.Result.Type.ID())
		parts := make([]string, len(definition.Inputs))
		for i, input := range definition.Inputs {
			parts[i] = fmt.Sprint(input.Type.ID())
		}
		next.Signature = catalog.ProcedureSignature(strings.Join(parts, ","))
		state.UnbindName(catalog.DescriptorNameKey(d))
		state.SetDescriptor(address, &next)
		state.BindName(catalog.DescriptorNameKey(&next), address)
	}
	if err := remap.Declarations(); err != nil {
		return err
	}
	// All declaration/type handles must be stable before mapping cross-routine
	// captures. These descriptor copies are private to this replacement state;
	// attaching payloads in place preserves the already-remapped call handles.
	for _, descriptor := range state.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || d.SQL == nil {
			continue
		}
		if d.SQLDefaults != nil {
			defaults, err := remap.Defaults(d.SQLDefaults)
			if err != nil {
				return err
			}
			d.SQLDefaults = defaults
		}
		if d.SQLBody != nil {
			program, err := remap.Body(d.SQLBody)
			if err != nil {
				return err
			}
			d.SQLBody = program
		}
	}
	return remap.Finish()
}
