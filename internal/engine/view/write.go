package view

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The requested relation remains the SQL/ACL namespace. This execution-owned
// rewrite carries physical authority separately; no dense slots are persisted.
type Write struct {
	Logical          analyze.Binding
	Layers           []WriteLayer
	BaseRelation     *catalog.Relation
	Ref              ids.ObjectRef
	outputs          []expr.Expr
	Predicates       []expr.Expr
	Checks           []CheckGroup
	QueryOccurrences map[viewWriteOccurrence]int
}
type WriteLayer struct {
	Relation       *catalog.Relation
	Ref            ids.ObjectRef
	Targets        []int
	Source         *catalog.Relation
	SourceRef      ids.ObjectRef
	MainOccurrence int
}
type CheckGroup struct {
	Relation   *catalog.Relation
	Predicates []expr.Expr
}

func viewWriteError(d *catalog.Relation, operation, reason string) error {
	verb, hint := "update", "updating"
	if operation == "insert" {
		verb, hint = "insert into", "inserting into"
	}
	if operation == "delete" {
		verb, hint = "delete from", "deleting from"
	}
	command := "UPDATE"
	if operation == "insert" {
		command = "INSERT"
	}
	if operation == "delete" {
		command = "DELETE"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf("cannot %s view %q", verb, d.Name()), Detail: reason, Hint: fmt.Sprintf("To enable %s the view, provide an INSTEAD OF %s trigger or an unconditional ON %s DO INSTEAD rule.", hint, command, command)}
}
func viewComputedWriteError(d *catalog.Relation, operation string, slot int) error {
	verb := "update"
	if operation == "insert" {
		verb = "insert into"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf("cannot %s column %q of view %q", verb, d.Columns[slot].Name, d.Name()), Detail: "View columns that are not columns of their base relation are not updatable."}
}

func StableViewSlot(source *catalog.Relation, leaf stored.ViewLeaf, frozen int) (int, error) {
	if frozen < 0 || frozen >= len(leaf.Attributes) {
		return 0, fmt.Errorf("stuntgres: invalid view write source slot")
	}
	for i, attr := range source.Attributes {
		if attr == leaf.Attributes[frozen] {
			return i, nil
		}
	}
	return 0, ids.ErrCatalogReplaced
}

func BindViewWrite(owner *catalog.Owner, view catalog.Read, b *analyze.Binding, stmt parser.Statement, consumed ...ids.ObjectRef) error {
	if b.Relation == nil || b.Relation.RelationKind != catalog.ViewRelation {
		return nil
	}
	operation := ""
	switch stmt.(type) {
	case *parser.Insert:
		operation = "insert"
	case *parser.Update:
		operation = "update"
	case *parser.Delete:
		operation = "delete"
	default:
		return nil
	}
	w := &Write{Logical: *b}
	w.Logical.ViewWrite = nil
	d, ref := b.Relation, b.Ref
	targets := append([]int(nil), b.Targets...)
	seen := make(map[ids.ObjectRef]bool)
	// outputs start in the logical target's canonical mutation frame.
	outputs := make([]expr.Expr, len(d.Columns))
	for i, c := range d.Columns {
		outputs[i] = plan.ColumnExpr{Info: expr.Info{Usage: c.Usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Slot: i}
	}
	cascaded := false
	for d.RelationKind == catalog.ViewRelation {
		match := false
		for _, rule := range stored.RulesOf(d) {
			if rule.Event() == stored.RuleEvent(stmt) && (len(consumed) == 0 || consumed[0] != ref) {
				match = true
				if rule.Instead() && rule.Condition() == nil {
					w.Checks = nil
				}
			}
		}
		if match {
			if len(w.Layers) == 0 {
				return nil
			}
			break
		}
		if seen[ref] {
			return sqlError("42P17", fmt.Sprintf("infinite recursion detected in rules for relation %q", d.Name()))
		}
		seen[ref] = true
		project, sourceNode, predicates, reason := stored.ViewWriteShape(d)
		if reason != "" {
			return viewWriteError(d, operation, reason)
		}
		if sourceNode.Occurrence < 0 || sourceNode.Occurrence >= len(stored.ViewOf(d).Sources()) {
			return ids.ErrCatalogReplaced
		}
		leaf := stored.ViewOf(d).Sources()[sourceNode.Occurrence]
		source, ok := view.Relation(leaf.Ref.Address)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		use, err := stored.ViewUseReference(view, d, leaf.Ref)
		if err != nil {
			return err
		}
		if source.RelationKind != catalog.TableRelation && source.RelationKind != catalog.ViewRelation && source.RelationKind != catalog.ForeignRelation {
			return viewWriteError(d, operation, "Views that do not select from a single table or view are not automatically updatable.")
		}
		direct := make([]int, len(project.Expressions))
		writable := false
		for i, expr := range project.Expressions {
			direct[i] = -1
			if c, ok := stored.ViewDirectColumn(expr); ok && stored.SameViewRelation(c.Origin, leaf.Ref) {
				slot, err := StableViewSlot(source, leaf, c.Slot)
				if err != nil {
					return err
				}
				direct[i] = slot
				writable = true
			}
		}
		if operation != "delete" && !writable {
			return viewWriteError(d, operation, "Views that have no updatable columns are not automatically updatable.")
		}
		for slot := range direct {
			if direct[slot] < 0 {
				for _, target := range targets {
					if target == slot {
						return viewComputedWriteError(d, operation, slot)
					}
				}
			}
		}
		mapped := make([]int, len(targets))
		for i, t := range targets {
			mapped[i] = direct[t]
			for j, prior := range mapped[:i] {
				if prior == mapped[i] {
					partial := false
					if b.Update != nil {
						partial = len(b.Update.ArrayTargets) != 0 && b.Update.ArrayTargets[i] != nil && b.Update.ArrayTargets[j] != nil
					} else {
						partial = len(b.ArrayTargets) != 0 && b.ArrayTargets[i] != nil && b.ArrayTargets[j] != nil
					}
					if !partial {
						return sqlError("42601", fmt.Sprintf("multiple assignments to same column %q", source.Columns[prior].Name))
					}
				}
			}
		}
		w.Layers = append(w.Layers, WriteLayer{Relation: d, Ref: ref, Targets: mapped, Source: source, SourceRef: use, MainOccurrence: sourceNode.Occurrence})
		var lowerStored func(expr.Expr, bool) (expr.Expr, bool, error)
		lowerStored = func(bound expr.Expr, frozenLayout bool) (expr.Expr, bool, error) {
			return stored.RewriteViewExpression(view, bound, d, func(n expr.Expr) (expr.Expr, bool, error) {
				if q, ok := n.(functions.StringVariadic); ok && exec.FoldOriginOf(q).Owner == (ids.ObjectRef{}) {
					q = q.WithOrigin(exec.FoldOrigin{Owner: ref, Definition: stored.ViewOf(d).RuleOID(), Revision: stored.ViewOf(d).DefinitionRevision()})
					q.RoutineRecipeReference = d
					var args []expr.Expr
					// A changed leaf exits the shared mapper before its children.
					// Lower nested calls and columns through that same mapper first.
					for i, arg := range q.Args {
						next, did, err := lowerStored(arg, frozenLayout)
						if err != nil {
							return nil, false, err
						}
						if did {
							if args == nil {
								args = append([]expr.Expr(nil), q.Args...)
							}
							args[i] = next
						}
					}
					if args != nil {
						q.Args = args
					}
					return q, true, nil
				}
				if !frozenLayout {
					// withGeneration just borrowed the current physical recipe.
					// Its stable slots must not be translated through the view's
					// older frozen leaf layout a second time.
					return n, false, nil
				}
				c, ok := n.(plan.ColumnExpr)
				if !ok || !stored.SameViewRelation(c.Origin, leaf.Ref) || c.Frame != project.Frame {
					return n, false, nil
				}
				slot, err := StableViewSlot(source, leaf, c.Slot)
				if err != nil {
					return nil, false, err
				}
				c.Slot = slot
				c.Origin = use
				c.Scope = 1
				c.Frame = 1
				// Main-source expressions now consume the physical destination row,
				// not a SELECT occurrence/window. Refresh virtual recipes against
				// its live attributes; independent subquery occurrences stay intact.
				c.Virtual = nil
				c.RowStart, c.RowWidth, c.QueryOccurrence = 0, 0, 0
				c = c.WithGeneration(source, slot, 0)
				c.RoutineRecipeReference = d
				if c.Virtual != nil {
					virtual, _, err := lowerStored(c.Virtual, false)
					if err != nil {
						return nil, false, err
					}
					c.Virtual = virtual
				}
				return c, true, nil
			})
		}
		expanded := make([]expr.Expr, len(project.Expressions))
		// Lower lazily: merely visiting unused expressions cannot run conversions.
		for i, expr := range project.Expressions {
			next, _, err := lowerStored(expr, true)
			if err != nil {
				return err
			}
			expanded[i] = next
		}
		replace := func(n expr.Expr) (expr.Expr, bool, error) {
			c, ok := n.(plan.ColumnExpr)
			if !ok || !stored.SameViewRelation(c.Origin, ref) || c.Frame > 1 {
				return n, false, nil
			}
			if c.Slot < 0 || c.Slot >= len(expanded) {
				return nil, false, ids.ErrCatalogReplaced
			}
			return expanded[c.Slot], true, nil
		}
		for i, expr := range outputs {
			next, _, err := stored.RewriteViewExpression(view, expr, nil, replace)
			if err != nil {
				return err
			}
			outputs[i] = next
		}
		for i, expr := range w.Predicates {
			next, _, err := stored.RewriteViewExpression(view, expr, nil, replace)
			if err != nil {
				return err
			}
			w.Predicates[i] = next
		}
		for i := range w.Checks {
			for j, expr := range w.Checks[i].Predicates {
				next, _, err := stored.RewriteViewExpression(view, expr, nil, replace)
				if err != nil {
					return err
				}
				w.Checks[i].Predicates[j] = next
			}
		}
		cascaded = cascaded || stored.ViewOf(d).CheckOption() == stored.CheckCascaded
		var checked []expr.Expr
		for _, expr := range predicates {
			next, _, err := lowerStored(expr, true)
			if err != nil {
				return err
			}
			w.Predicates = append(w.Predicates, next)
			if operation != "delete" && (cascaded || stored.ViewOf(d).CheckOption() != stored.CheckNone) {
				checked = append(checked, next)
			}
		}
		if len(checked) != 0 {
			w.Checks = append(w.Checks, CheckGroup{Relation: d, Predicates: checked})
		}
		targets = mapped
		d = source
		ref = use
	}
	for _, rule := range stored.RulesOf(d) {
		if rule.Event() == stored.RuleEvent(stmt) && rule.Instead() && rule.Condition() == nil && (len(consumed) == 0 || consumed[0] != ref) {
			w.Checks = nil
			break
		}
	}
	w.BaseRelation, w.Ref, w.outputs = d, ref, outputs
	b.ViewWrite = ViewWriteSlotOf(w)
	b.Targets = targets
	return lowerViewWrite(owner, view, b, stmt)
}

func lowerViewWrite(owner *catalog.Owner, view catalog.Read, b *analyze.Binding, stmt parser.Statement) error {
	w := ViewWriteOf(b.ViewWrite)
	logicalWidth, physicalWidth := len(w.Logical.Relation.Columns), len(w.BaseRelation.Columns)
	graft := func(root plan.Node, mapper func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
		return w.GraftQuery(b, view, root, mapper)
	}
	lowerMapped := func(bound expr.Expr) (expr.Expr, bool, error) {
		return stored.RewriteViewExpression(view, bound, nil, func(n expr.Expr) (expr.Expr, bool, error) {
			switch n.(type) {
			case plan.SubqueryExpr:
				b.SubqueryDemand = true
			case plan.ExtensionPathsExpr:
				// This binding flag owns nonquery catalog/role capture in
				// executeBoundCommandLocked; Binder.Consumer.CatalogSnapshot is
				// analysis-only and is not a field of binding.
				b.Prepared.RegclassConcat = true
			case functions.Binary:
				node := n.(functions.Binary)
				if !node.Plan.Immutable() {
					demand := true
					for _, arg := range node.Args {
						if exec.RandomLiteralNull(arg) {
							demand = false
							break
						}
					}
					b.VisibilitySnapshot = b.VisibilitySnapshot || demand
				}
			case expr.SnapshotDemander:
				_, catalogText, visibility := n.(expr.SnapshotDemander).SnapshotDemand(exec.RandomLiteralNull)
				b.Prepared.RegclassConcat = b.Prepared.RegclassConcat || catalogText
				b.VisibilitySnapshot = b.VisibilitySnapshot || visibility
			}
			old, absent := false, false
			var c plan.ColumnExpr
			switch x := n.(type) {
			case plan.ColumnExpr:
				c = x
			case plan.ReturningColumnExpr:
				c = x.ColumnExpr
				old, absent = x.Old, x.Absent
			default:
				return n, false, nil
			}
			if stored.SameViewRelation(c.Origin, w.Logical.Ref) && c.Frame == 1 {
				if c.Slot < 0 || c.Slot >= len(w.outputs) {
					return nil, false, ids.ErrCatalogReplaced
				}
				output := w.outputs[c.Slot]
				output, _, err := stored.RewriteViewExpression(view, output, nil, func(child expr.Expr) (expr.Expr, bool, error) {
					column, ok := child.(plan.ColumnExpr)
					if !ok || !stored.SameViewRelation(column.Origin, w.Ref) || column.Frame != 1 {
						return child, false, nil
					}
					column.Scope = c.Scope
					column.Frame = c.Frame
					return column, true, nil
				}, graft)
				if old || absent {
					return plan.ReturningColumnExpr{ColumnExpr: c, Old: old, Absent: absent, Value: output}, true, err
				}
				return output, true, err
			}
			if c.Scope == 1 && c.Frame == 1 && c.Slot >= logicalWidth {
				c.Slot += physicalWidth - logicalWidth
				return c, true, nil
			}
			return n, false, nil
		}, graft)
	}
	lower := func(expr expr.Expr) (expr.Expr, error) {
		next, _, err := lowerMapped(expr)
		return next, err
	}
	// Every bound in a mixed field/array path uses the physical write frame.
	// Preserve unchanged plans and copy only the ancestry of a lowered bound.
	var lowerTarget func(*plan.ArrayAssignmentTarget) (*plan.ArrayAssignmentTarget, error)
	lowerTarget = func(target *plan.ArrayAssignmentTarget) (*plan.ArrayAssignmentTarget, error) {
		if target == nil {
			return nil, nil
		}
		node, changed, err := plan.MapSubscript(target.Node, lowerMapped)
		if err != nil {
			return nil, err
		}
		next, err := lowerTarget(target.Next)
		if err != nil {
			return nil, err
		}
		if !changed && next == target.Next {
			return target, nil
		}
		copy := *target
		copy.Node, copy.Next = node, next
		return &copy, nil
	}
	var err error
	if _, inserting := stmt.(*parser.Insert); !inserting {
		predicates := make([]expr.Expr, len(w.Predicates))
		for i, x := range w.Predicates {
			predicates[i], _, err = stored.RewriteViewExpression(view, x, nil, func(n expr.Expr) (expr.Expr, bool, error) { return n, false, nil }, graft)
			if err != nil {
				return err
			}
		}
		w.Predicates = predicates
	}
	for i := range w.Checks {
		for j, x := range w.Checks[i].Predicates {
			w.Checks[i].Predicates[j], _, err = stored.RewriteViewExpression(view, x, nil, func(n expr.Expr) (expr.Expr, bool, error) { return n, false, nil }, graft)
			if err != nil {
				return err
			}
		}
	}
	if b.Update != nil {
		u := *b.Update
		u.Assignments = append([]expr.Expr(nil), u.Assignments...)
		for i, x := range u.Assignments {
			u.Assignments[i], err = lower(x)
			if err != nil {
				return err
			}
		}
		if len(u.ArrayTargets) != 0 {
			var replacements []*plan.ArrayAssignmentTarget
			for i, target := range u.ArrayTargets {
				next, failure := lowerTarget(target)
				if failure != nil {
					return failure
				}
				if next != target {
					if replacements == nil {
						replacements = append([]*plan.ArrayAssignmentTarget(nil), u.ArrayTargets...)
					}
					replacements[i] = next
				}
			}
			if replacements != nil {
				u.ArrayTargets = replacements
			}
		}
		u.Predicate, err = lower(u.Predicate)
		if err != nil {
			return err
		}
		u.Predicate = ViewWritePredicate(w.Predicates, u.Predicate)
		b.Update = &u
	} else if _, ok := stmt.(*parser.Delete); ok {
		b.Predicate, err = lower(b.Predicate)
		if err != nil {
			return err
		}
		b.Predicate = ViewWritePredicate(w.Predicates, b.Predicate)
	}
	if b.Returning != nil {
		r := *b.Returning
		r.Expressions = append([]expr.Expr(nil), r.Expressions...)
		for i, x := range r.Expressions {
			r.Expressions[i], err = lower(x)
			if err != nil {
				return err
			}
		}
		b.Returning = &r
	}
	if b.Insert.Conflict != nil {
		conflict := *b.Insert.Conflict
		conflict.Targets = b.Targets
		conflict.Keys = append([]analyze.ConflictKey(nil), conflict.Keys...)
		for i, key := range conflict.Keys {
			if key.Attribute != 0 {
				slot := -1
				for j, attr := range w.Logical.Relation.Attributes {
					if attr == key.Attribute {
						slot = j
						break
					}
				}
				if slot < 0 {
					return ids.ErrCatalogReplaced
				}
				output, ok := stored.ViewDirectColumn(w.outputs[slot])
				if !ok {
					return sqlError("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification")
				}
				conflict.Keys[i].Attribute = w.BaseRelation.Attributes[output.Slot]
			} else if key.Expression != nil {
				conflict.Keys[i].Expression, err = lower(key.Expression)
				if err != nil {
					return err
				}
			}
		}
		conflict.Predicate, err = lower(conflict.Predicate)
		if err != nil {
			return err
		}
		b.Insert.Conflict = &conflict
	}
	if insert, ok := stmt.(*parser.Insert); ok {
		return lowerViewInsert(owner, view, b, insert)
	}
	if _, ok := stmt.(*parser.Update); ok {
		for _, slot := range b.Targets {
			name := w.BaseRelation.Columns[slot].Name
			attr := w.BaseRelation.Attributes[slot]
			if _, generated := w.BaseRelation.ColumnMeta.Generations[attr]; generated {
				return analyze.GenerationWriteError(name, true)
			}
			if identity, ok := w.BaseRelation.ColumnMeta.Identities[attr]; ok && identity.Mode == parser.IdentityAlways {
				d := sequence.IdentityInsertError(name).(*sqlstate.Error)
				d.Message = fmt.Sprintf("column %q can only be updated to DEFAULT", name)
				d.Hint = ""
				return d
			}
			for i, target := range b.Targets {
				if target == slot && b.Update.Assignments[i] == nil {
					pos := parser.Position{}
					if len(b.Update.Origins) != 0 {
						pos = b.Update.Origins[i]
					} else {
						pos = stmt.(*parser.Update).Assignments[i].Value.SourcePosition()
					}
					return expr.ErrorAt("0A000", "UPDATE DEFAULT is unsupported", pos)
				}
			}
		}
	}
	return nil
}

// Required view quals precede user work, innermost first, including after a row
// wait. SQL UNKNOWN is rejected by the existing updateMatches TRUE-only rule.
func ViewWritePredicate(predicates []expr.Expr, user expr.Expr) expr.Expr {
	if len(predicates) == 0 {
		return user
	}
	return plan.ViewQualificationExpr{Info: expr.InfoOf(predicates[0]), Predicates: predicates, User: user}
}
