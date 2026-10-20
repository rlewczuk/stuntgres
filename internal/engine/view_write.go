package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The requested relation remains the SQL/ACL namespace. This execution-owned
// rewrite carries physical authority separately; no dense slots are persisted.
type viewWrite struct {
	logical          binding
	layers           []viewWriteLayer
	base             *catalog.Relation
	ref              ids.ObjectRef
	outputs          []expr.Expr
	predicates       []expr.Expr
	checks           []viewCheckGroup
	queryOccurrences map[viewWriteOccurrence]int
}
type viewWriteLayer struct {
	relation       *catalog.Relation
	ref            ids.ObjectRef
	targets        []int
	source         *catalog.Relation
	sourceRef      ids.ObjectRef
	mainOccurrence int
}
type viewCheckGroup struct {
	relation   *catalog.Relation
	predicates []expr.Expr
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

// Only admitted, analyzed nodes participate. A scalar subquery is a separate
// query boundary and must not be mistaken for a top-level aggregate/window.
func viewWriteShape(d *catalog.Relation) (*plan.ProjectNode, *plan.SourceNode, []expr.Expr, string) {
	q := viewOf(d).syntax
	if q.Distinct != nil {
		return nil, nil, nil, "Views containing DISTINCT are not automatically updatable."
	}
	if q.GroupBy != nil {
		return nil, nil, nil, "Views containing GROUP BY are not automatically updatable."
	}
	if q.Having != nil {
		return nil, nil, nil, "Views containing HAVING are not automatically updatable."
	}
	if q.Set != nil {
		return nil, nil, nil, "Views containing UNION, INTERSECT, or EXCEPT are not automatically updatable."
	}
	if q.Bounds != nil {
		return nil, nil, nil, "Views containing LIMIT or OFFSET are not automatically updatable."
	}
	p, ok := viewOf(d).query.Root.(*plan.ProjectNode)
	if !ok {
		return nil, nil, nil, "Views that do not select from a single table or view are not automatically updatable."
	}
	var predicates []expr.Expr
	hasTargetSet := p.HasTargetSet
	node := p.Child
	for {
		switch n := node.(type) {
		case *plan.SetProjectNode:
			hasTargetSet = true
			node = n.Child
		case *plan.AggregateNode:
			return nil, nil, nil, "Views that return aggregate functions are not automatically updatable."
		case *plan.WindowNode:
			return nil, nil, nil, "Views that return window functions are not automatically updatable."
		case *plan.SortNode:
			node = n.Child
		case *plan.RowLockNode:
			node = n.Child
		case *plan.FilterNode:
			predicates = append(predicates, n.Predicate)
			node = n.Child
		case *plan.SourceNode:
			if hasTargetSet {
				return nil, nil, nil, "Views that return set-returning functions are not automatically updatable."
			}
			// Frozen-layout dump replay wraps ordinary sources in aliases.
			from := q.From
			for {
				alias, ok := from.(*parser.AliasReference)
				if !ok {
					break
				}
				from = alias.Input
			}
			if _, ordinary := from.(*parser.TableReference); !ordinary {
				return nil, nil, nil, "Views that do not select from a single table or view are not automatically updatable."
			}
			return p, n, predicates, ""
		default:
			if hasTargetSet {
				return nil, nil, nil, "Views that return set-returning functions are not automatically updatable."
			}
			return nil, nil, nil, "Views that do not select from a single table or view are not automatically updatable."
		}
	}
}

func viewDirectColumn(expr expr.Expr) (plan.ColumnExpr, bool) {
	if c, ok := expr.(plan.ColumnExpr); ok {
		return c, true
	}
	if c, ok := expr.(plan.CoercionExpr); ok && c.ResultUsage() == c.Child.ResultUsage() {
		return viewDirectColumn(c.Child)
	}
	return plan.ColumnExpr{}, false
}
func stableViewSlot(source *catalog.Relation, leaf viewLeaf, frozen int) (int, error) {
	if frozen < 0 || frozen >= len(leaf.attributes) {
		return 0, fmt.Errorf("stuntgres: invalid view write source slot")
	}
	for i, attr := range source.Attributes {
		if attr == leaf.attributes[frozen] {
			return i, nil
		}
	}
	return 0, ids.ErrCatalogReplaced
}
func sameViewRelation(a, b ids.ObjectRef) bool {
	a.Address.SubID = 0
	b.Address.SubID = 0
	return a == b
}

func (e *Engine) bindViewWrite(b *binding, auth AuthorizationView, stmt parser.Statement, consumed ...ids.ObjectRef) error {
	if b.relation == nil || b.relation.RelationKind != catalog.ViewRelation {
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
	w := &viewWrite{logical: *b}
	w.logical.viewWrite = nil
	d, ref := b.relation, b.ref
	targets := append([]int(nil), b.targets...)
	seen := make(map[ids.ObjectRef]bool)
	// outputs start in the logical target's canonical mutation frame.
	outputs := make([]expr.Expr, len(d.Columns))
	for i, c := range d.Columns {
		outputs[i] = plan.ColumnExpr{Info: expr.Info{Usage: c.Usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Slot: i}
	}
	cascaded := false
	for d.RelationKind == catalog.ViewRelation {
		match := false
		for _, rule := range rulesOf(d) {
			if rule.event == ruleEvent(stmt) && (len(consumed) == 0 || consumed[0] != ref) {
				match = true
				if rule.instead && rule.condition == nil {
					w.checks = nil
				}
			}
		}
		if match {
			if len(w.layers) == 0 {
				return nil
			}
			break
		}
		if seen[ref] {
			return sqlError("42P17", fmt.Sprintf("infinite recursion detected in rules for relation %q", d.Name()))
		}
		seen[ref] = true
		project, sourceNode, predicates, reason := viewWriteShape(d)
		if reason != "" {
			return viewWriteError(d, operation, reason)
		}
		if sourceNode.Occurrence < 0 || sourceNode.Occurrence >= len(viewOf(d).sources) {
			return ids.ErrCatalogReplaced
		}
		leaf := viewOf(d).sources[sourceNode.Occurrence]
		source, ok := auth.Catalog.Relation(leaf.ref.Address)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		use, err := storedViewUseReference(auth.Catalog, d, leaf.ref)
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
			if c, ok := viewDirectColumn(expr); ok && sameViewRelation(c.Origin, leaf.ref) {
				slot, err := stableViewSlot(source, leaf, c.Slot)
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
					if b.update != nil {
						partial = len(b.update.arrayTargets) != 0 && b.update.arrayTargets[i] != nil && b.update.arrayTargets[j] != nil
					} else {
						partial = len(b.arrayTargets) != 0 && b.arrayTargets[i] != nil && b.arrayTargets[j] != nil
					}
					if !partial {
						return sqlError("42601", fmt.Sprintf("multiple assignments to same column %q", source.Columns[prior].Name))
					}
				}
			}
		}
		w.layers = append(w.layers, viewWriteLayer{relation: d, ref: ref, targets: mapped, source: source, sourceRef: use, mainOccurrence: sourceNode.Occurrence})
		var lowerStored func(expr.Expr, bool) (expr.Expr, bool, error)
		lowerStored = func(bound expr.Expr, frozenLayout bool) (expr.Expr, bool, error) {
			return rewriteViewExpression(auth.Catalog, bound, d, func(n expr.Expr) (expr.Expr, bool, error) {
				if q, ok := n.(functions.StringVariadic); ok && quoteOriginOf(q).Owner == (ids.ObjectRef{}) {
					q = q.WithOrigin(exec.QuoteOrigin{Owner: ref, Definition: viewOf(d).rule, Revision: viewOf(d).quoteRevision})
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
				if !ok || !sameViewRelation(c.Origin, leaf.ref) || c.Frame != project.Frame {
					return n, false, nil
				}
				slot, err := stableViewSlot(source, leaf, c.Slot)
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
			if !ok || !sameViewRelation(c.Origin, ref) || c.Frame > 1 {
				return n, false, nil
			}
			if c.Slot < 0 || c.Slot >= len(expanded) {
				return nil, false, ids.ErrCatalogReplaced
			}
			return expanded[c.Slot], true, nil
		}
		for i, expr := range outputs {
			next, _, err := rewriteViewExpression(auth.Catalog, expr, nil, replace)
			if err != nil {
				return err
			}
			outputs[i] = next
		}
		for i, expr := range w.predicates {
			next, _, err := rewriteViewExpression(auth.Catalog, expr, nil, replace)
			if err != nil {
				return err
			}
			w.predicates[i] = next
		}
		for i := range w.checks {
			for j, expr := range w.checks[i].predicates {
				next, _, err := rewriteViewExpression(auth.Catalog, expr, nil, replace)
				if err != nil {
					return err
				}
				w.checks[i].predicates[j] = next
			}
		}
		cascaded = cascaded || viewOf(d).checkOption == viewCheckCascaded
		var checked []expr.Expr
		for _, expr := range predicates {
			next, _, err := lowerStored(expr, true)
			if err != nil {
				return err
			}
			w.predicates = append(w.predicates, next)
			if operation != "delete" && (cascaded || viewOf(d).checkOption != viewCheckNone) {
				checked = append(checked, next)
			}
		}
		if len(checked) != 0 {
			w.checks = append(w.checks, viewCheckGroup{relation: d, predicates: checked})
		}
		targets = mapped
		d = source
		ref = use
	}
	for _, rule := range rulesOf(d) {
		if rule.event == ruleEvent(stmt) && rule.instead && rule.condition == nil && (len(consumed) == 0 || consumed[0] != ref) {
			w.checks = nil
			break
		}
	}
	w.base, w.ref, w.outputs = d, ref, outputs
	b.viewWrite = w
	b.targets = targets
	return e.lowerViewWrite(b, auth, stmt)
}

func rewriteViewExpression(view catalog.Read, bound expr.Expr, recipe *catalog.Relation, leaf func(expr.Expr) (expr.Expr, bool, error), queries ...func(plan.Node, func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error)) (expr.Expr, bool, error) {
	var mapper func(expr.Expr) (expr.Expr, bool, error)
	mapper = func(n expr.Expr) (expr.Expr, bool, error) {
		return mapBoundExpression(view, n, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(root plan.Node) (plan.Node, error) {
			if len(queries) != 0 {
				return queries[0](root, mapper)
			}
			return plan.RewriteQueryExpressions(root, mapper)
		}, recipe, leaf)
	}
	return mapper(bound)
}

func (e *Engine) lowerViewWrite(b *binding, auth AuthorizationView, stmt parser.Statement) error {
	w := b.viewWrite
	logicalWidth, physicalWidth := len(w.logical.relation.Columns), len(w.base.Columns)
	graft := func(root plan.Node, mapper func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
		return w.graftQuery(b, auth.Catalog, root, mapper)
	}
	lowerMapped := func(bound expr.Expr) (expr.Expr, bool, error) {
		return rewriteViewExpression(auth.Catalog, bound, nil, func(n expr.Expr) (expr.Expr, bool, error) {
			switch n.(type) {
			case plan.SubqueryExpr:
				b.subqueryDemand = true
			case plan.ExtensionPathsExpr:
				// This binding flag owns nonquery catalog/role capture in
				// executeBoundCommandLocked; expressionBinder.catalogSnapshot is
				// analysis-only and is not a field of binding.
				b.prepared.regclassConcat = true
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
					b.visibilitySnapshot = b.visibilitySnapshot || demand
				}
			case expr.SnapshotDemander:
				_, catalogText, visibility := n.(expr.SnapshotDemander).SnapshotDemand(exec.RandomLiteralNull)
				b.prepared.regclassConcat = b.prepared.regclassConcat || catalogText
				b.visibilitySnapshot = b.visibilitySnapshot || visibility
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
			if sameViewRelation(c.Origin, w.logical.ref) && c.Frame == 1 {
				if c.Slot < 0 || c.Slot >= len(w.outputs) {
					return nil, false, ids.ErrCatalogReplaced
				}
				output := w.outputs[c.Slot]
				output, _, err := rewriteViewExpression(auth.Catalog, output, nil, func(child expr.Expr) (expr.Expr, bool, error) {
					column, ok := child.(plan.ColumnExpr)
					if !ok || !sameViewRelation(column.Origin, w.ref) || column.Frame != 1 {
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
		node, changed, err := mapSubscript(target.Node, lowerMapped)
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
		predicates := make([]expr.Expr, len(w.predicates))
		for i, x := range w.predicates {
			predicates[i], _, err = rewriteViewExpression(auth.Catalog, x, nil, func(n expr.Expr) (expr.Expr, bool, error) { return n, false, nil }, graft)
			if err != nil {
				return err
			}
		}
		w.predicates = predicates
	}
	for i := range w.checks {
		for j, x := range w.checks[i].predicates {
			w.checks[i].predicates[j], _, err = rewriteViewExpression(auth.Catalog, x, nil, func(n expr.Expr) (expr.Expr, bool, error) { return n, false, nil }, graft)
			if err != nil {
				return err
			}
		}
	}
	if b.update != nil {
		u := *b.update
		u.assignments = append([]expr.Expr(nil), u.assignments...)
		for i, x := range u.assignments {
			u.assignments[i], err = lower(x)
			if err != nil {
				return err
			}
		}
		if len(u.arrayTargets) != 0 {
			var replacements []*plan.ArrayAssignmentTarget
			for i, target := range u.arrayTargets {
				next, failure := lowerTarget(target)
				if failure != nil {
					return failure
				}
				if next != target {
					if replacements == nil {
						replacements = append([]*plan.ArrayAssignmentTarget(nil), u.arrayTargets...)
					}
					replacements[i] = next
				}
			}
			if replacements != nil {
				u.arrayTargets = replacements
			}
		}
		u.predicate, err = lower(u.predicate)
		if err != nil {
			return err
		}
		u.predicate = viewWritePredicate(w.predicates, u.predicate)
		b.update = &u
	} else if _, ok := stmt.(*parser.Delete); ok {
		b.predicate, err = lower(b.predicate)
		if err != nil {
			return err
		}
		b.predicate = viewWritePredicate(w.predicates, b.predicate)
	}
	if b.returning != nil {
		r := *b.returning
		r.expressions = append([]expr.Expr(nil), r.expressions...)
		for i, x := range r.expressions {
			r.expressions[i], err = lower(x)
			if err != nil {
				return err
			}
		}
		b.returning = &r
	}
	if b.insert.conflict != nil {
		conflict := *b.insert.conflict
		conflict.targets = b.targets
		conflict.keys = append([]conflictKey(nil), conflict.keys...)
		for i, key := range conflict.keys {
			if key.attribute != 0 {
				slot := -1
				for j, attr := range w.logical.relation.Attributes {
					if attr == key.attribute {
						slot = j
						break
					}
				}
				if slot < 0 {
					return ids.ErrCatalogReplaced
				}
				output, ok := viewDirectColumn(w.outputs[slot])
				if !ok {
					return sqlError("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification")
				}
				conflict.keys[i].attribute = w.base.Attributes[output.Slot]
			} else if key.expression != nil {
				conflict.keys[i].expression, err = lower(key.expression)
				if err != nil {
					return err
				}
			}
		}
		conflict.predicate, err = lower(conflict.predicate)
		if err != nil {
			return err
		}
		b.insert.conflict = &conflict
	}
	if insert, ok := stmt.(*parser.Insert); ok {
		return e.lowerViewInsert(b, auth, insert)
	}
	if _, ok := stmt.(*parser.Update); ok {
		for _, slot := range b.targets {
			name := w.base.Columns[slot].Name
			attr := w.base.Attributes[slot]
			if _, generated := w.base.ColumnMeta.Generations[attr]; generated {
				return generationWriteError(name, true)
			}
			if identity, ok := w.base.ColumnMeta.Identities[attr]; ok && identity.Mode == parser.IdentityAlways {
				d := sequence.IdentityInsertError(name).(*sqlstate.Error)
				d.Message = fmt.Sprintf("column %q can only be updated to DEFAULT", name)
				d.Hint = ""
				return d
			}
			for i, target := range b.targets {
				if target == slot && b.update.assignments[i] == nil {
					pos := parser.Position{}
					if len(b.update.origins) != 0 {
						pos = b.update.origins[i]
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
func viewWritePredicate(predicates []expr.Expr, user expr.Expr) expr.Expr {
	if len(predicates) == 0 {
		return user
	}
	return plan.ViewQualificationExpr{Info: expr.InfoOf(predicates[0]), Predicates: predicates, User: user}
}
