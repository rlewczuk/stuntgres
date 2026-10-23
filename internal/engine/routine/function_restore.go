package routine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RestoreDirective defers SQL function body validation during Restore; dumps
// emit it before their SQL routines.
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
// Preserve each complete Usage; collation is the caller's destination capability
// mapper, nil only when no collation identity remapping is needed.
func RebindSQLDefinitions(view catalog.Read, state *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress, collation func(sqltype.CollationRef) (sqltype.CollationRef, error)) error {
	for address, descriptor := range state.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || d.SQL == nil {
			continue
		}
		definition := *d.SQL
		definition.Formals = make([]proc.Formal, len(d.SQL.Formals))
		mapUsage := func(u sqltype.Usage) (sqltype.Usage, error) {
			a := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())}
			if mapped, ok := mapping[a]; ok {
				a = mapped
			}
			t, ok := view.LookupType(sqltype.ID(a.ObjectID))
			if !ok || !t.Concrete() {
				return sqltype.Usage{}, fmt.Errorf("missing imported SQL routine type")
			}
			u.Type = t
			if collation != nil && u.Collation.ID != 0 {
				var err error
				u.Collation, err = collation(u.Collation)
				if err != nil {
					return sqltype.Usage{}, err
				}
			}
			return u, nil
		}
		var err error
		for i, formal := range d.SQL.Formals {
			definition.Formals[i] = formal
			definition.Formals[i].Usage, err = mapUsage(formal.Usage)
			if err != nil {
				return err
			}
		}
		if err = definition.SetFormals(definition.Formals); err != nil {
			return err
		}
		definition.Result, err = mapUsage(d.SQL.Result)
		if err != nil {
			return err
		}
		if d.SQL.PL != nil {
			program := *d.SQL.PL
			program.Variables = append([]proc.PLVariable(nil), d.SQL.PL.Variables...)
			for i, variable := range program.Variables {
				if variable.Record {
					continue
				}
				mapped, failure := mapUsage(variable.Usage)
				if failure != nil {
					return failure
				}
				program.Variables[i].Usage = mapped
			}
			definition.PL = &program
		}
		next := *d
		next.SQL = &definition
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
	// All declaration/type handles must be stable before mapping cross-routine
	// captures. These descriptor copies are private to this replacement state;
	// attaching payloads in place preserves the already-remapped call handles.
	for _, descriptor := range state.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || d.SQL == nil {
			continue
		}
		if d.SQLDefaults != nil {
			defaults, err := d.SQLDefaults.Rebind(view, mapping)
			if err != nil {
				return err
			}
			d.SQLDefaults = defaults
		}
		if d.SQLBody != nil {
			program, err := d.SQLBody.Rebind(view, mapping)
			if err != nil {
				return err
			}
			d.SQLBody = program
		}
	}
	return nil
}
