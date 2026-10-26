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

// planCreateTrigger checks, in order: the relation, the TRIGGER grant, the
// timing and relation kind, the events, a name collision without OR REPLACE,
// the function and its EXECUTE grant, then the WHEN condition.
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
	if s.Timing != "before" || !s.Row {
		return RelationChange{}, sqlError("0A000", "only row-level BEFORE triggers are supported")
	}
	if old.RelationKind != catalog.TableRelation && old.RelationKind != catalog.PartitionedRelation && old.RelationKind != catalog.ForeignRelation {
		return RelationChange{}, analyze.TableKindError(old, "change")
	}
	p := stored.TriggerParts{Name: s.Name.Text, Relation: ref, Arguments: s.Arguments}
	if err := triggerEvents(&p, old, s.Events); err != nil {
		return RelationChange{}, err
	}
	list := stored.TriggersOf(old)
	index := slices.IndexFunc(list, func(t stored.Trigger) bool { return t.Name() == p.Name })
	if index >= 0 && !s.Replace {
		return RelationChange{}, sqlError("42710", fmt.Sprintf("trigger %q for relation %q already exists", p.Name, old.Name()))
	}
	address, err := triggerFunction(h, auth, catalogAccess, s.Function)
	if err != nil {
		return RelationChange{}, err
	}
	p.Procedure = catalogAccess.Ref(address)
	if err := catalogAccess.Check(auth, access.Requirement{Object: p.Procedure, Privileges: authorization.ExecutePrivilege}); err != nil {
		return RelationChange{}, err
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
	return change, err
}

// triggerEvents records the INSERT, UPDATE [OF columns] and DELETE events of
// p, rejecting TRUNCATE, a repeated event and a missing column in order.
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
		default:
			return sqlError("0A000", "TRUNCATE triggers are unsupported")
		}
		if p.Events&bit != 0 {
			return sqlError("42601", "same event specified more than once")
		}
		p.Events |= bit
		for _, column := range event.Columns {
			slot, ok := old.Index[column.Text]
			if !ok {
				return expr.ErrorAt("42703", fmt.Sprintf("column %q of relation %q does not exist", column.Text, old.Name()), column.Pos)
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
		return ids.ObjectAddress{}, sqlError("42809", fmt.Sprintf("%s() is a procedure", catalog.ObjectNameText(name)))
	}
	if !ok || d.SQL == nil || d.SQL.Invocation != proc.DMLTriggerInvocation {
		return ids.ObjectAddress{}, sqlError("42P17", fmt.Sprintf("function %s must return type trigger", catalog.ObjectNameText(name)))
	}
	return address, nil
}

// triggerWhen binds, validates and freezes the WHEN condition of p, and adds
// the objects it depends on to p.References.
func triggerWhen(h Host, auth ddl.Authority, catalogAccess access.View, p *stored.TriggerParts, old *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) error {
	ns := &analyze.RuleNamespace{Relation: old, Ref: p.Relation, Event: "UPDATE", Trigger: true, TriggerInsert: p.Events&stored.TriggerInsert != 0, TriggerDelete: p.Events&stored.TriggerDelete != 0}
	condition, query, binding, err := h.BindTriggerWhen(auth, ns, raw, input)
	if err != nil {
		return err
	}
	if err := triggerWhenError(condition, p.Events, old); err != nil {
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
func triggerWhenError(condition expr.Expr, events stored.TriggerEvent, old *catalog.Relation) error {
	var invalid error
	plan.WalkExpression(condition, func(node expr.Expr) {
		if invalid != nil {
			return
		}
		switch node := node.(type) {
		case plan.SubqueryExpr:
			invalid = expr.ErrorAt("0A000", "cannot use subquery in trigger WHEN condition", node.SourcePosition())
		case plan.ColumnExpr:
			if node.Scope != analyze.RulePseudoScope {
				return
			}
			if node.Frame == 1 && events&stored.TriggerInsert != 0 {
				invalid = expr.ErrorAt("42P17", "INSERT trigger's WHEN condition cannot reference OLD values", node.SourcePosition())
			}
			if node.Frame == 2 && events&stored.TriggerDelete != 0 {
				invalid = expr.ErrorAt("42P17", "DELETE trigger's WHEN condition cannot reference NEW values", node.SourcePosition())
			}
			if node.Frame == 2 {
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
