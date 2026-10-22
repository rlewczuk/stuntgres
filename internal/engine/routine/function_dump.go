package routine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

// SQLRoutineDumpEvents adds each SQL routine's declaration event, preceded by
// the events of its input and result types.
func SQLRoutineDumpEvents(r dump.Resolver, view catalog.Read, routines []*catalog.Procedure, events dump.Graph) error {
	for _, d := range routines {
		key := dump.Event{OID: d.Address().ObjectID}
		events[key] = nil
		for i := range len(d.SQL.Formals) + 1 {
			usage := d.SQL.Result
			if i < len(d.SQL.Formals) {
				usage = d.SQL.Formals[i].Usage
			}
			predecessor, err := r.Predecessor(view, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())}, d.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				events[key] = append(events[key], dump.Event{OID: predecessor})
			}
		}
		if d.SQLBody != nil {
			for _, dependency := range d.SQLBody.Dependencies() {
				address := dependency.Ref.Address
				address.SubID = 0
				if address == d.Address() {
					continue
				}
				predecessor, err := r.Predecessor(view, address, d.Address())
				if err != nil {
					return err
				}
				if predecessor != 0 {
					events[key] = append(events[key], dump.Event{OID: predecessor})
				}
			}
		}
		if d.SQLDefaults != nil {
			for _, dependency := range d.SQLDefaults.Dependencies() {
				address := dependency.Ref.Address
				address.SubID = 0
				predecessor, err := r.Predecessor(view, address, d.Address())
				if err != nil {
					return err
				}
				if predecessor != 0 {
					events[key] = append(events[key], dump.Event{OID: predecessor})
				}
			}
		}
	}
	return nil
}

// SQLRoutineDumpStatement renders a SQL routine as CREATE FUNCTION, owned by
// the routine owner.
func SQLRoutineDumpStatement(p dump.Presenter, view catalog.Read, roles authorization.Read, d *catalog.Procedure) (dump.Statement, error) {
	owner, err := roles.Descriptor(d.Owner)
	if err != nil {
		return dump.Statement{}, err
	}
	namespace, ok := view.Namespace(d.Namespace())
	if !ok {
		return dump.Statement{}, fmt.Errorf("missing SQL routine namespace")
	}
	if d.ACL.Explicit {
		return dump.Statement{}, fmt.Errorf("stuntgres: dump cannot serialize explicit routine ACL")
	}
	var sql strings.Builder
	kind := "FUNCTION"
	if d.RoutineKind == catalog.ProcedureRoutine {
		kind = "PROCEDURE"
	}
	sql.WriteString("CREATE " + kind + " " + catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(d.Name()) + "(")
	for i, formal := range d.SQL.Formals {
		if i != 0 {
			sql.WriteString(", ")
		}
		switch formal.Mode {
		case "o":
			sql.WriteString("OUT ")
		case "b":
			sql.WriteString("INOUT ")
		case "v":
			sql.WriteString("VARIADIC ")
		}
		if formal.Name != "" {
			sql.WriteString(catalog.QuoteIdentifier(formal.Name) + " ")
		}
		name, err := p.ColumnDeclaration(view, catalog.Column{Usage: formal.Usage})
		if err != nil {
			return dump.Statement{}, err
		}
		sql.WriteString(name)
		if formal.HasDefault {
			if d.SQLDefaults == nil {
				return dump.Statement{}, fmt.Errorf("missing routine declaration defaults")
			}
			text, err := d.SQLDefaults.DefaultSQL(context.Background(), view, d.SQL.FormalInputs[i])
			if err != nil {
				return dump.Statement{}, err
			}
			sql.WriteString(" DEFAULT " + text)
		}
	}
	sql.WriteString(")")
	if d.RoutineKind != catalog.ProcedureRoutine {
		result, err := p.ColumnDeclaration(view, catalog.Column{Usage: d.SQL.Result})
		if err != nil {
			return dump.Statement{}, err
		}
		sql.WriteString(" RETURNS " + result)
	}
	sql.WriteString(" LANGUAGE SQL ")
	if d.SQL.Standard {
		if d.SQLBody == nil {
			return dump.Statement{}, fmt.Errorf("missing checked SQL-standard routine body")
		}
		if err := d.SQLBody.ValidateDefinition(d); err != nil {
			return dump.Statement{}, err
		}
		if err := d.SQLBody.Validate(view); err != nil {
			return dump.Statement{}, err
		}
		sql.WriteString("BEGIN ATOMIC\n")
		for i := range d.SQLBody.Len() {
			statement, err := d.SQLBody.StatementSQL(context.Background(), view, i)
			if err != nil {
				return dump.Statement{}, err
			}
			sql.WriteString(statement + ";\n")
		}
		sql.WriteString("END;\n")
	} else {
		sql.WriteString("AS " + dump.CommentLiteral(d.SQL.Source) + ";\n")
	}
	return dump.Statement{Owner: owner.Name, SQL: sql.String()}, nil
}
