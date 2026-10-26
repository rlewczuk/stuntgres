package routine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
)

// RoutineDumpEvents orders owned SQL bodies and real transition aggregates by
// their actual type/support/body predecessors.
func RoutineDumpEvents(r dump.Resolver, view catalog.Read, routines []*catalog.Procedure, events dump.Graph) error {
	for _, d := range routines {
		key := dump.Event{OID: d.Address().ObjectID}
		events[key] = nil
		appendPredecessor := func(address ids.ObjectAddress) error {
			address.SubID = 0
			if address.ObjectID == 0 || address == d.Address() {
				return nil
			}
			predecessor, err := r.Predecessor(view, address, d.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				events[key] = append(events[key], dump.Event{OID: predecessor})
			}
			return nil
		}
		// Replay uses the typed declaration, including OUT formals and its
		// result, independently of the ordinary semantic definition graph.
		if d.SQL != nil {
			for i := range len(d.SQL.Formals) + 1 {
				usage := d.SQL.Result
				if i < len(d.SQL.Formals) {
					usage = d.SQL.Formals[i].Usage
				}
				if err := appendPredecessor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())}); err != nil {
					return err
				}
			}
		}
		// Declaration prerequisites order replay, not pg_depend/DROP edges.
		for _, address := range d.DeclarationDependencies {
			if err := appendPredecessor(address); err != nil {
				return err
			}
		}
		var failure error
		catalog.VisitProcedureDefinitionDependencies(d, func(address ids.ObjectAddress) bool {
			// Typed declaration prerequisites above already cover these types.
			// Other body/default types still come from the shared visitor.
			if address.ClassID == ids.TypeClassID && d.SQL != nil {
				if address.ObjectID == ids.OID(d.SQL.Result.Type.ID()) {
					return true
				}
				for _, formal := range d.SQL.Formals {
					if address.ObjectID == ids.OID(formal.Usage.Type.ID()) {
						return true
					}
				}
			}
			failure = appendPredecessor(address)
			return failure == nil
		})
		if failure != nil {
			return failure
		}
	}
	return nil
}

// RoutineDumpStatement renders an owned callable under its actual owner.
func RoutineDumpStatement(p dump.Presenter, view catalog.Read, roles authorization.Read, d *catalog.Procedure) (dump.Statement, error) {
	if d.Aggregate != nil {
		return aggregateDumpStatement(p, view, roles, d)
	}
	owner, err := roles.Descriptor(d.Owner)
	if err != nil {
		return dump.Statement{}, err
	}
	namespace, ok := view.Namespace(d.Namespace())
	if !ok {
		return dump.Statement{}, fmt.Errorf("missing SQL routine namespace")
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
		result := "pg_catalog.trigger"
		if d.SQL.Invocation == proc.EventTriggerInvocation {
			result = "pg_catalog.event_trigger"
		} else if d.SQL.Invocation == proc.OrdinaryInvocation {
			var err error
			result, err = p.ColumnDeclaration(view, catalog.Column{Usage: d.SQL.Result})
			if err != nil {
				return dump.Statement{}, err
			}
		}
		sql.WriteString(" RETURNS " + result)
	}
	if d.Strict {
		sql.WriteString(" STRICT")
	}
	language, ok := view.Get(d.Language).(*catalog.Language)
	if !ok || (catalog.LanguageProvider{}).Validate(view, language) != nil {
		return dump.Statement{}, fmt.Errorf("missing routine language authority")
	}
	languageName := catalog.QuoteIdentifier(language.Name())
	if language.Name() == "sql" {
		languageName = "SQL"
	}
	sql.WriteString(" LANGUAGE " + languageName + " ")
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
