package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const sqlRoutineRestoreDirective = "SET check_function_bodies = false;"

func sqlRoutineDumpEvents(view catalog.Read, routines []*catalog.Procedure, events map[dumpEventKey][]dumpEventKey) error {
	for _, d := range routines {
		key := dumpEventKey{oid: d.Address().ObjectID}
		events[key] = nil
		for i := range len(d.SQL.Inputs) + 1 {
			usage := d.SQL.Result
			if i < len(d.SQL.Inputs) {
				usage = d.SQL.Inputs[i]
			}
			predecessor, err := dumpPredecessor(view, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())}, d.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				events[key] = append(events[key], dumpEventKey{oid: predecessor})
			}
		}
	}
	return nil
}

func sqlRoutineDumpStatement(view catalog.Read, roles authorization.Read, d *catalog.Procedure) (dumpDomain, error) {
	owner, err := roles.Descriptor(d.Owner)
	if err != nil {
		return dumpDomain{}, err
	}
	namespace, ok := view.Namespace(d.Namespace())
	if !ok {
		return dumpDomain{}, fmt.Errorf("missing SQL routine namespace")
	}
	if d.ACL.Explicit {
		return dumpDomain{}, fmt.Errorf("stuntgres: dump cannot serialize explicit routine ACL")
	}
	var sql strings.Builder
	sql.WriteString("CREATE FUNCTION " + catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(d.Name()) + "(")
	for i, input := range d.SQL.Inputs {
		if i != 0 {
			sql.WriteString(", ")
		}
		name, err := catalogColumnDeclaration(view, catalog.Column{Usage: input})
		if err != nil {
			return dumpDomain{}, err
		}
		sql.WriteString(name)
	}
	result, err := catalogColumnDeclaration(view, catalog.Column{Usage: d.SQL.Result})
	if err != nil {
		return dumpDomain{}, err
	}
	sql.WriteString(") RETURNS " + result + " LANGUAGE SQL AS " + commentLiteral(d.SQL.Source) + ";\n")
	return dumpDomain{owner.Name, sql.String()}, nil
}

// Match the exact pg_dump-compatible directive, not arbitrary SET expressions.
// This flag exists on Restore's private operation only, never live settings.
func sqlRoutineReplayDirective(data string, stmt parser.Statement) bool {
	s, ok := stmt.(*parser.Set)
	if !ok || s.Local || s.Default || s.Name.Text != "check_function_bodies" || len(s.Values) != 1 || s.Values[0] != "false" {
		return false
	}
	start := s.Pos.Offset
	return start >= 0 && len(data)-start >= len(sqlRoutineRestoreDirective) && data[start:start+len(sqlRoutineRestoreDirective)] == sqlRoutineRestoreDirective
}

func (e *Engine) validateRestoredSQLRoutines(ctx context.Context, op *session.Operation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	if index := auth.Catalog.State().ProcedureNames(); index == nil || len(index.SQLNames) == 0 {
		return nil
	}
	input, _ := sessionPresentation(op.Session)
	for _, descriptor := range auth.Catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if d, ok := descriptor.(*catalog.Procedure); ok && d.SQL != nil {
			auth.State.CurrentUser = d.Owner
			if _, _, err = e.bindSQLRoutineLocked(op, auth, d, input, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// Typed SQL definitions are recreated against the destination type generation;
// syntax is immutable and text-body names remain runtime analysis, not edges.
func rebindSQLRoutineDefinitions(view catalog.Read, state *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	for address, descriptor := range state.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || d.SQL == nil {
			continue
		}
		definition := *d.SQL
		definition.Inputs = make([]sqltype.Usage, len(d.SQL.Inputs))
		mapUsage := func(u sqltype.Usage) (sqltype.Usage, error) {
			a := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())}
			if mapped, ok := mapping[a]; ok {
				a = mapped
			}
			t, ok := view.LookupType(sqltype.ID(a.ObjectID))
			if !ok || !t.Concrete() {
				return sqltype.Usage{}, fmt.Errorf("missing imported SQL routine type")
			}
			return t.Usage(), nil
		}
		var err error
		for i, input := range d.SQL.Inputs {
			definition.Inputs[i], err = mapUsage(input)
			if err != nil {
				return err
			}
		}
		definition.Result, err = mapUsage(d.SQL.Result)
		if err != nil {
			return err
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
	return nil
}
