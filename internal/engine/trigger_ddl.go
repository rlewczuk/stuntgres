package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func (e *Engine) executeTrigger(scope *StatementScope, s *parser.CreateTrigger, count int) (Result, error) {
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	auth, err := e.statementAuthorizationLocked(scope.op, s)
	if err != nil {
		return fail(err)
	}
	h := ddlHost{e: e, op: scope.op}
	ref, err := h.ResolveRelation(auth, s.Table)
	if err != nil {
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	old := auth.Catalog.MustRelation(ref.Address)
	if err := auth.Check(access.Requirement{Object: ref, Privileges: authorization.TriggerPrivilege}); err != nil {
		return fail(err)
	}
	if s.Timing != "before" || !s.Row {
		return fail(sqlError("0A000", "only row-level BEFORE triggers are supported"))
	}
	if old.RelationKind != catalog.TableRelation && old.RelationKind != catalog.PartitionedRelation && old.RelationKind != catalog.ForeignRelation {
		return fail(analyze.TableKindError(old, "change"))
	}
	p := stored.TriggerParts{Name: s.Name.Text, Relation: ref, Arguments: s.Arguments}
	for _, event := range s.Events {
		var bit stored.TriggerEvent
		switch event.Name {
		case "insert":
			bit = stored.TriggerInsert
		case "update":
			bit = stored.TriggerUpdate
		case "delete":
			bit = stored.TriggerDelete
		default:
			return fail(sqlError("0A000", "TRUNCATE triggers are unsupported"))
		}
		if p.Events&bit != 0 {
			return fail(sqlError("42601", "same event specified more than once"))
		}
		p.Events |= bit
		for _, column := range event.Columns {
			slot, ok := old.Index[column.Text]
			if !ok {
				return fail(expr.ErrorAt("42703", fmt.Sprintf("column %q of relation %q does not exist", column.Text, old.Name()), column.Pos))
			}
			p.UpdateColumns = append(p.UpdateColumns, old.Attributes[slot])
		}
	}
	list := stored.TriggersOf(old)
	index := slices.IndexFunc(list, func(t stored.Trigger) bool { return t.Name() == p.Name })
	if index >= 0 && !s.Replace {
		return fail(sqlError("42710", fmt.Sprintf("trigger %q for relation %q already exists", p.Name, old.Name())))
	}
	identity, n, err := h.SelectFunction(auth, s.Function, "")
	if err != nil {
		return fail(err)
	}
	if n != 1 {
		return fail(sqlError("42883", fmt.Sprintf("function %s() does not exist", catalog.ObjectNameText(s.Function))))
	}
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID}
	d, ok := auth.Catalog.Get(address).(*catalog.Procedure)
	if ok && d.RoutineKind == catalog.ProcedureRoutine {
		return fail(sqlError("42809", fmt.Sprintf("%s() is a procedure", catalog.ObjectNameText(s.Function))))
	}
	if !ok || d.SQL == nil || !d.SQL.Trigger {
		return fail(sqlError("42P17", fmt.Sprintf("function %s must return type trigger", catalog.ObjectNameText(s.Function))))
	}
	p.Procedure = auth.Ref(address)
	if err := auth.Check(access.Requirement{Object: p.Procedure, Privileges: authorization.ExecutePrivilege}); err != nil {
		return fail(err)
	}
	p.References = []ids.ObjectAddress{address}
	for _, attribute := range p.UpdateColumns {
		column := ref.Address
		column.SubID = attribute
		p.References = append(p.References, column)
	}
	if s.When != nil {
		ns := &analyze.RuleNamespace{Relation: old, Ref: ref, Event: "UPDATE", Trigger: true, TriggerInsert: p.Events&stored.TriggerInsert != 0, TriggerDelete: p.Events&stored.TriggerDelete != 0}
		condition, query, binding, err := h.BindTriggerWhen(auth, ns, s.When, scope.input)
		if err != nil {
			return fail(err)
		}
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
				if node.Frame == 1 && p.Events&stored.TriggerInsert != 0 {
					invalid = expr.ErrorAt("42P17", "INSERT trigger's WHEN condition cannot reference OLD values", node.SourcePosition())
				}
				if node.Frame == 2 && p.Events&stored.TriggerDelete != 0 {
					invalid = expr.ErrorAt("42P17", "DELETE trigger's WHEN condition cannot reference NEW values", node.SourcePosition())
				}
				if node.Frame == 2 {
					if _, generated := old.ColumnMeta.Generations[node.Origin.Address.SubID]; generated {
						invalid = expr.ErrorAt("42P17", "BEFORE trigger's WHEN condition cannot reference NEW generated columns", node.SourcePosition())
					}
				}
			}
		})
		if invalid != nil {
			return fail(invalid)
		}
		p.When, p.Binding = condition, &binding
		p.Presentation, err = stored.FreezeQueryPresentation(auth.Catalog, h.TemporaryOwner(auth), query, binding, false, false, 0, "", false)
		if err != nil {
			return fail(err)
		}
		refs, err := stored.ExpressionDependRefs(scope.op.Context(), auth.Catalog, condition)
		if err != nil {
			return fail(err)
		}
		p.References = append(p.References, refs...)
	}
	if index >= 0 {
		p.OID = list[index].OID()
	} else {
		p.OID, err = h.AllocateOID()
		if err != nil {
			return fail(err)
		}
	}
	definition, err := stored.NewTrigger(p)
	if err != nil {
		return fail(err)
	}
	next := *old
	list = slices.Clone(list)
	if index >= 0 {
		list[index] = definition
	} else {
		list = append(list, definition)
	}
	slices.SortFunc(list, func(a, b stored.Trigger) int { return strings.Compare(a.Name(), b.Name()) })
	next.Triggers = stored.TriggersPayload(list)
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return fail(err)
	}
	var replacement triggerReplacement
	if s.Replace {
		replacement = triggerReplacement{Relation: next.Address(), OID: definition.OID()}
	}
	return e.publishRelationChangeLocked(scope, auth, view.RelationChange{Ref: ref, Next: &next}, replacement)
}
