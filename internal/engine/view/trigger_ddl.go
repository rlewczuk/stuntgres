package view

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PlanTrigger decides CREATE [OR REPLACE] TRIGGER, ALTER TRIGGER (RENAME and
// [NO] DEPENDS ON EXTENSION) and DROP TRIGGER: it resolves the relation
// (locked is the admitted identity), checks authority, and returns the
// relation with its trigger list changed and a new revision. An IF EXISTS
// miss returns only the NOTICE text in Skip.
func PlanTrigger(h Host, auth ddl.Authority, stmt parser.Statement, locked ids.ObjectRef, input sqltype.InputEnvironment) (RelationChange, error) {
	if s, ok := stmt.(*parser.CreateTrigger); ok {
		return planCreateTrigger(h, auth, s, locked, input)
	}
	return planTriggerLifecycle(h, auth, stmt, locked)
}

// planCreateTrigger checks relation-kind and timing restrictions before binding
// forbidden WHEN/OF forms, then resolves the function before name collisions.
func planCreateTrigger(h Host, auth ddl.Authority, s *parser.CreateTrigger, locked ids.ObjectRef, input sqltype.InputEnvironment) (RelationChange, error) {
	ref, err := h.ResolveRelation(auth, s.Table)
	if err != nil {
		return RelationChange{}, err
	}
	if ref != locked {
		return RelationChange{}, ids.ErrCatalogReplaced
	}
	catalogAccess := h.AccessView(auth)
	old := catalogAccess.Catalog.MustRelation(ref.Address)
	if err := catalogAccess.Check(auth, access.Requirement{Object: ref, Privileges: authorization.TriggerPrivilege}); err != nil {
		return RelationChange{}, err
	}
	if err := triggerKind(old, s); err != nil {
		return RelationChange{}, err
	}
	p := stored.TriggerParts{Name: s.Name.Text, Mode: catalog.TriggerOrigin, Relation: ref, Arguments: s.Arguments}
	p.Timing, p.Level = stored.TriggerAfter, stored.TriggerStatement
	if s.Timing == "before" {
		p.Timing = stored.TriggerBefore
	} else if s.Timing == "instead" {
		p.Timing = stored.TriggerInstead
	}
	if s.Row {
		p.Level = stored.TriggerRow
	}
	if err := triggerEvents(&p, old, s.Events); err != nil {
		return RelationChange{}, err
	}
	list := stored.TriggersOf(old)
	index := slices.IndexFunc(list, func(t stored.Trigger) bool { return t.Name() == p.Name })
	address, err := triggerFunction(h, auth, catalogAccess, s.Function)
	if err != nil {
		return RelationChange{}, err
	}
	p.Procedure = catalogAccess.Ref(address)
	if err := catalogAccess.Check(auth, access.Requirement{Object: p.Procedure, Privileges: authorization.ExecutePrivilege}); err != nil {
		return RelationChange{}, err
	}
	if index >= 0 && !s.Replace {
		return RelationChange{}, sqlError("42710", fmt.Sprintf("trigger %q for relation %q already exists", p.Name, old.Name()))
	}
	p.References = []ids.ObjectAddress{address}
	for _, attribute := range p.UpdateColumns {
		column := ref.Address
		column.SubID = attribute
		p.References = append(p.References, column)
	}
	if s.When != nil {
		if err := triggerWhen(h, auth, catalogAccess, &p, old, s.When, input); err != nil {
			return RelationChange{}, err
		}
	}
	if index >= 0 {
		p.OID = list[index].OID()
	} else if p.OID, err = h.AllocateOID(); err != nil {
		return RelationChange{}, err
	}
	definition, err := stored.NewTrigger(p)
	if err != nil {
		return RelationChange{}, err
	}
	list = slices.Clone(list)
	if index >= 0 {
		list[index] = definition
	} else {
		list = append(list, definition)
	}
	slices.SortFunc(list, func(a, b stored.Trigger) int { return strings.Compare(a.Name(), b.Name()) })
	change, err := triggerListChange(h, ref, old, list, nil)
	if err == nil && s.Replace {
		change.Replacement = TriggerReplacement{Relation: change.Next.Address(), OID: definition.OID()}
	}
	change.CommandAddress = ids.ObjectAddress{ClassID: 2620, ObjectID: definition.OID()}
	change.CommandTag = "CREATE TRIGGER"
	return change, err
}

// triggerKind preserves CREATE TRIGGER diagnostic precedence without binding
// expressions or looking up columns prohibited at the INSTEAD OF boundary.
func triggerKind(old *catalog.Relation, s *parser.CreateTrigger) error {
	kind, detail := "", ""
	switch old.RelationKind {
	case catalog.ViewRelation:
		kind = "view"
		for _, event := range s.Events {
			if event.Name == "truncate" {
				detail = "Views cannot have TRUNCATE triggers."
				break
			}
		}
		if detail == "" && s.Row && s.Timing != "instead" {
			detail = "Views cannot have row-level BEFORE or AFTER triggers."
		}
	case catalog.TableRelation, catalog.PartitionedRelation:
		if s.Timing == "instead" {
			kind, detail = "table", "Tables cannot have INSTEAD OF triggers."
		}
	case catalog.ForeignRelation:
		if s.Timing == "instead" {
			kind, detail = "foreign table", "Foreign tables cannot have INSTEAD OF triggers."
		}
	default:
		return analyze.TableKindError(old, "change")
	}
	if detail != "" {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf("%q is a %s", old.Name(), kind), Detail: detail}
	}
	if s.Timing == "instead" {
		if !s.Row {
			return sqlError("0A000", "INSTEAD OF triggers must be FOR EACH ROW")
		}
		if s.When != nil {
			return sqlError("0A000", "INSTEAD OF triggers cannot have WHEN conditions")
		}
		for _, event := range s.Events {
			if len(event.Columns) != 0 {
				return sqlError("0A000", "INSTEAD OF triggers cannot have column lists")
			}
		}
	} else if s.Timing != "after" && (s.Timing != "before" || !s.Row) {
		return sqlError("0A000", "only row-level BEFORE and row or statement AFTER triggers are supported")
	}
	return nil
}

// triggerEvents records events and validates their orientation and columns.
func triggerEvents(p *stored.TriggerParts, old *catalog.Relation, events []parser.TriggerEvent) error {
	for _, event := range events {
		var bit stored.TriggerEvent
		switch event.Name {
		case "insert":
			bit = stored.TriggerInsert
		case "update":
			bit = stored.TriggerUpdate
		case "delete":
			bit = stored.TriggerDelete
		case "truncate":
			if p.Level == stored.TriggerRow {
				return sqlError("0A000", "TRUNCATE FOR EACH ROW triggers are not supported")
			}
			bit = stored.TriggerTruncate
		default:
			return sqlError("0A000", "unsupported trigger event")
		}
		p.Events |= bit
		for _, column := range event.Columns {
			slot, ok := old.Index[column.Text]
			if !ok {
				return sqlError("42703", fmt.Sprintf("column %q of relation %q does not exist", column.Text, old.Name()))
			}
			p.UpdateColumns = append(p.UpdateColumns, old.Attributes[slot])
		}
	}
	return nil
}

// triggerFunction selects the zero-argument trigger function name and
// returns its address; a procedure or a non-trigger function is rejected.
func triggerFunction(h Host, auth ddl.Authority, catalogAccess access.View, name parser.ObjectName) (ids.ObjectAddress, error) {
	identity, n, err := h.SelectFunction(auth, name, "")
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	if n != 1 {
		return ids.ObjectAddress{}, sqlError("42883", fmt.Sprintf("function %s() does not exist", catalog.ObjectNameText(name)))
	}
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID}
	d, ok := catalogAccess.Catalog.Get(address).(*catalog.Procedure)
	if ok && d.RoutineKind == catalog.ProcedureRoutine {
		return ids.ObjectAddress{}, sqlError("42883", fmt.Sprintf("function %s() does not exist", catalog.ObjectNameText(name)))
	}
	if !ok || d.SQL == nil || d.SQL.Invocation != proc.DMLTriggerInvocation {
		return ids.ObjectAddress{}, sqlError("42P17", fmt.Sprintf("function %s must return type trigger", catalog.ObjectNameText(name)))
	}
	return address, nil
}

// triggerWhen binds, validates and freezes the WHEN condition of p, and adds
// the objects it depends on to p.References.
func triggerWhen(h Host, auth ddl.Authority, catalogAccess access.View, p *stored.TriggerParts, old *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) error {
	ns := &analyze.RuleNamespace{Relation: old, Ref: p.Relation, Event: "UPDATE", Trigger: true, TriggerAfter: p.Timing == stored.TriggerAfter, TriggerStatement: p.Level == stored.TriggerStatement, TriggerInsert: p.Events&stored.TriggerInsert != 0, TriggerDelete: p.Events&stored.TriggerDelete != 0}
	condition, query, binding, err := h.BindTriggerWhen(auth, ns, raw, input)
	if err != nil {
		return err
	}
	if err := triggerWhenError(condition, *p, old); err != nil {
		return err
	}
	p.When, p.Binding = condition, &binding
	p.Presentation, err = stored.FreezeQueryPresentation(catalogAccess.Catalog, h.TemporaryOwner(auth), query, binding, false, false, 0, "", false)
	if err != nil {
		return err
	}
	refs, err := stored.ExpressionDependRefs(h.Context(), catalogAccess.Catalog, condition)
	if err != nil {
		return err
	}
	p.References = append(p.References, refs...)
	return nil
}

// triggerWhenError returns the first subquery, or OLD/NEW reference the
// events forbid, in walk order.
func triggerWhenError(condition expr.Expr, p stored.TriggerParts, old *catalog.Relation) error {
	var invalid error
	plan.WalkExpression(condition, func(node expr.Expr) {
		if invalid != nil {
			return
		}
		switch node := node.(type) {
		case plan.SubqueryExpr:
			invalid = expr.ErrorAt("0A000", "cannot use subquery in trigger WHEN condition", node.SourcePosition())
		case plan.RowExpr:
			if node.RuleSide == 0 {
				return
			}
			if p.Level == stored.TriggerStatement {
				invalid = expr.ErrorAt("42P17", "statement trigger's WHEN condition cannot reference column values", node.SourcePosition())
			} else if node.RuleSide == 1 && p.Events&stored.TriggerInsert != 0 {
				invalid = expr.ErrorAt("42P17", "INSERT trigger's WHEN condition cannot reference OLD values", node.SourcePosition())
			} else if node.RuleSide == 2 && p.Events&stored.TriggerDelete != 0 {
				invalid = expr.ErrorAt("42P17", "DELETE trigger's WHEN condition cannot reference NEW values", node.SourcePosition())
			} else if node.RuleSide == 2 && p.Timing == stored.TriggerBefore && len(old.ColumnMeta.Generations) != 0 {
				invalid = expr.ErrorAt("42P17", "BEFORE trigger's WHEN condition cannot reference NEW generated columns", node.SourcePosition())
			}
		case plan.ColumnExpr:
			if node.Scope != analyze.RulePseudoScope {
				return
			}
			if p.Level == stored.TriggerStatement {
				invalid = expr.ErrorAt("42P17", "statement trigger's WHEN condition cannot reference column values", node.SourcePosition())
				return
			}
			if node.Frame == 1 && p.Events&stored.TriggerInsert != 0 {
				invalid = expr.ErrorAt("42P17", "INSERT trigger's WHEN condition cannot reference OLD values", node.SourcePosition())
			}
			if node.Frame == 2 && p.Events&stored.TriggerDelete != 0 {
				invalid = expr.ErrorAt("42P17", "DELETE trigger's WHEN condition cannot reference NEW values", node.SourcePosition())
			}
			if node.Frame == 2 && p.Timing == stored.TriggerBefore {
				if _, generated := old.ColumnMeta.Generations[node.Origin.Address.SubID]; generated {
					invalid = expr.ErrorAt("42P17", "BEFORE trigger's WHEN condition cannot reference NEW generated columns", node.SourcePosition())
				}
			}
		}
	})
	return invalid
}

// triggerListChange is old with its trigger list replaced by list and a new
// revision.
func triggerListChange(h Host, ref ids.ObjectRef, old *catalog.Relation, list []stored.Trigger, sources []catalog.Descriptor) (RelationChange, error) {
	next := *old
	next.Triggers = stored.TriggersPayload(list)
	var err error
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return RelationChange{}, err
	}
	return RelationChange{Ref: ref, Next: &next, Sources: sources}, nil
}
