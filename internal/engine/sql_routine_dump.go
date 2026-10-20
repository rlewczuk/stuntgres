package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

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
