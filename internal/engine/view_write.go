package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The requested relation remains the SQL/ACL namespace. This execution-owned
// rewrite carries physical authority separately; no dense slots are persisted.
type viewWrite struct {
	logical          binding
	layers           []viewWriteLayer
	base             *relationDescriptor
	ref              ObjectRef
	outputs          []BoundExpr
	predicates       []BoundExpr
	checks           []viewCheckGroup
	queryOccurrences map[viewWriteOccurrence]int
}
type viewWriteLayer struct {
	relation       *relationDescriptor
	ref            ObjectRef
	targets        []int
	source         *relationDescriptor
	sourceRef      ObjectRef
	mainOccurrence int
}
type viewCheckGroup struct {
	relation   *relationDescriptor
	predicates []BoundExpr
}

func viewWriteError(d *relationDescriptor, operation, reason string) error {
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
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf("cannot %s view %q", verb, d.name), Detail: reason, Hint: fmt.Sprintf("To enable %s the view, provide an INSTEAD OF %s trigger or an unconditional ON %s DO INSTEAD rule.", hint, command, command)}
}
func viewComputedWriteError(d *relationDescriptor, operation string, slot int) error {
	verb := "update"
	if operation == "insert" {
		verb = "insert into"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf("cannot %s column %q of view %q", verb, d.columns[slot].Name, d.name), Detail: "View columns that are not columns of their base relation are not updatable."}
}

// Only admitted, analyzed nodes participate. A scalar subquery is a separate
// query boundary and must not be mistaken for a top-level aggregate/window.
func viewWriteShape(d *relationDescriptor) (*projectNode, *sourceNode, []BoundExpr, string) {
	q := d.view.syntax
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
	p, ok := d.view.query.Root.(*projectNode)
	if !ok {
		return nil, nil, nil, "Views that do not select from a single table or view are not automatically updatable."
	}
	var predicates []BoundExpr
	node := p.child
	for {
		switch n := node.(type) {
		case *aggregateNode:
			return nil, nil, nil, "Views that return aggregate functions are not automatically updatable."
		case *windowNode:
			return nil, nil, nil, "Views that return window functions are not automatically updatable."
		case *sortNode:
			node = n.child
		case *rowLockNode:
			node = n.child
		case *filterNode:
			predicates = append(predicates, n.predicate)
			node = n.child
		case *sourceNode:
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
			return nil, nil, nil, "Views that do not select from a single table or view are not automatically updatable."
		}
	}
}

func viewDirectColumn(expr BoundExpr) (columnExpression, bool) {
	if c, ok := expr.(columnExpression); ok {
		return c, true
	}
	if c, ok := expr.(coercionExpression); ok && c.ResultUsage() == c.child.ResultUsage() {
		return viewDirectColumn(c.child)
	}
	return columnExpression{}, false
}
func stableViewSlot(source *relationDescriptor, leaf viewLeaf, frozen int) (int, error) {
	if frozen < 0 || frozen >= len(leaf.attributes) {
		return 0, fmt.Errorf("stuntgres: invalid view write source slot")
	}
	for i, attr := range source.attributes {
		if attr == leaf.attributes[frozen] {
			return i, nil
		}
	}
	return 0, ErrCatalogReplaced
}
func sameViewRelation(a, b ObjectRef) bool {
	a.Address.SubID = 0
	b.Address.SubID = 0
	return a == b
}

func (e *Engine) bindViewWrite(b *binding, auth AuthorizationView, stmt parser.Statement) error {
	if b.relation == nil || b.relation.relationKind != viewRelation {
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
	seen := make(map[ObjectRef]bool)
	// outputs start in the logical target's canonical mutation frame.
	outputs := make([]BoundExpr, len(d.columns))
	for i, c := range d.columns {
		outputs[i] = columnExpression{expressionInfo: expressionInfo{usage: c.Usage, properties: ExprProperties{RowDependent: true}}, origin: ref, slot: i}
	}
	cascaded := false
	for d.relationKind == viewRelation {
		if seen[ref] {
			return sqlError("42P17", fmt.Sprintf("infinite recursion detected in rules for relation %q", d.name))
		}
		seen[ref] = true
		project, sourceNode, predicates, reason := viewWriteShape(d)
		if reason != "" {
			return viewWriteError(d, operation, reason)
		}
		if sourceNode.occurrence < 0 || sourceNode.occurrence >= len(d.view.sources) {
			return ErrCatalogReplaced
		}
		leaf := d.view.sources[sourceNode.occurrence]
		source, ok := auth.catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
		if !ok {
			return ErrCatalogReplaced
		}
		use, err := storedViewUseReference(auth.catalog, d, leaf.ref)
		if err != nil {
			return err
		}
		if source.relationKind != tableRelation && source.relationKind != viewRelation {
			return viewWriteError(d, operation, "Views that do not select from a single table or view are not automatically updatable.")
		}
		direct := make([]int, len(project.expressions))
		writable := false
		for i, expr := range project.expressions {
			direct[i] = -1
			if c, ok := viewDirectColumn(expr); ok && sameViewRelation(c.origin, leaf.ref) {
				slot, err := stableViewSlot(source, leaf, c.slot)
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
						return sqlError("42601", fmt.Sprintf("multiple assignments to same column %q", source.columns[prior].Name))
					}
				}
			}
		}
		w.layers = append(w.layers, viewWriteLayer{relation: d, ref: ref, targets: mapped, source: source, sourceRef: use, mainOccurrence: sourceNode.occurrence})
		lowerStored := func(expr BoundExpr) (BoundExpr, bool, error) {
			return rewriteViewExpression(auth.catalog, expr, func(n BoundExpr) (BoundExpr, bool, error) {
				c, ok := n.(columnExpression)
				if !ok || !sameViewRelation(c.origin, leaf.ref) || c.frame != project.frame {
					return n, false, nil
				}
				slot, err := stableViewSlot(source, leaf, c.slot)
				if err != nil {
					return nil, false, err
				}
				c.slot = slot
				c.origin = use
				c.scope = 1
				c.frame = 1
				// Main-source expressions now consume the physical destination row,
				// not a SELECT occurrence/window. Refresh virtual recipes against
				// its live attributes; independent subquery occurrences stay intact.
				c.virtual = nil
				c.rowStart, c.rowWidth, c.queryOccurrence = 0, 0, 0
				c = c.withGeneration(source, slot, 0)
				return c, true, nil
			})
		}
		expanded := make([]BoundExpr, len(project.expressions))
		// Lower lazily: merely visiting unused expressions cannot run conversions.
		for i, expr := range project.expressions {
			next, _, err := lowerStored(expr)
			if err != nil {
				return err
			}
			expanded[i] = next
		}
		replace := func(n BoundExpr) (BoundExpr, bool, error) {
			c, ok := n.(columnExpression)
			if !ok || !sameViewRelation(c.origin, ref) || c.frame > 1 {
				return n, false, nil
			}
			if c.slot < 0 || c.slot >= len(expanded) {
				return nil, false, ErrCatalogReplaced
			}
			return expanded[c.slot], true, nil
		}
		for i, expr := range outputs {
			next, _, err := rewriteViewExpression(auth.catalog, expr, replace)
			if err != nil {
				return err
			}
			outputs[i] = next
		}
		for i, expr := range w.predicates {
			next, _, err := rewriteViewExpression(auth.catalog, expr, replace)
			if err != nil {
				return err
			}
			w.predicates[i] = next
		}
		for i := range w.checks {
			for j, expr := range w.checks[i].predicates {
				next, _, err := rewriteViewExpression(auth.catalog, expr, replace)
				if err != nil {
					return err
				}
				w.checks[i].predicates[j] = next
			}
		}
		cascaded = cascaded || d.view.checkOption == viewCheckCascaded
		var checked []BoundExpr
		for _, expr := range predicates {
			next, _, err := lowerStored(expr)
			if err != nil {
				return err
			}
			w.predicates = append(w.predicates, next)
			if operation != "delete" && (cascaded || d.view.checkOption != viewCheckNone) {
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
	w.base, w.ref, w.outputs = d, ref, outputs
	b.viewWrite = w
	b.targets = targets
	return e.lowerViewWrite(b, auth, stmt)
}

func rewriteViewExpression(view CatalogRead, expr BoundExpr, leaf func(BoundExpr) (BoundExpr, bool, error), queries ...func(LogicalNode, func(BoundExpr) (BoundExpr, bool, error)) (LogicalNode, error)) (BoundExpr, bool, error) {
	var mapper func(BoundExpr) (BoundExpr, bool, error)
	mapper = func(n BoundExpr) (BoundExpr, bool, error) {
		return mapBoundExpression(view, n, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(root LogicalNode) (LogicalNode, error) {
			if len(queries) != 0 {
				return queries[0](root, mapper)
			}
			return rewriteQueryExpressions(root, mapper)
		}, leaf)
	}
	return mapper(expr)
}

func (e *Engine) lowerViewWrite(b *binding, auth AuthorizationView, stmt parser.Statement) error {
	w := b.viewWrite
	logicalWidth, physicalWidth := len(w.logical.relation.columns), len(w.base.columns)
	graft := func(root LogicalNode, mapper func(BoundExpr) (BoundExpr, bool, error)) (LogicalNode, error) {
		return w.graftQuery(b, auth.catalog, root, mapper)
	}
	lower := func(expr BoundExpr) (BoundExpr, error) {
		next, _, err := rewriteViewExpression(auth.catalog, expr, func(n BoundExpr) (BoundExpr, bool, error) {
			switch n.(type) {
			case subqueryExpression:
				b.subqueryDemand = true
			case visibilityInquiryExpression:
				b.visibilitySnapshot = true
			}
			old := false
			var c columnExpression
			switch x := n.(type) {
			case columnExpression:
				c = x
			case updateOldExpression:
				c = x.columnExpression
				old = true
			case deleteNewExpression:
				if sameViewRelation(x.origin, w.logical.ref) {
					return literalExpression{expressionInfo: x.expressionInfo, value: sqltype.Null(x.usage.Type)}, true, nil
				}
				return n, false, nil
			default:
				return n, false, nil
			}
			if sameViewRelation(c.origin, w.logical.ref) && c.frame == 1 {
				if c.slot < 0 || c.slot >= len(w.outputs) {
					return nil, false, ErrCatalogReplaced
				}
				output := w.outputs[c.slot]
				output, _, err := rewriteViewExpression(auth.catalog, output, func(child BoundExpr) (BoundExpr, bool, error) {
					column, ok := child.(columnExpression)
					if !ok || !sameViewRelation(column.origin, w.ref) || column.frame != 1 {
						return child, false, nil
					}
					column.scope = c.scope
					column.frame = c.frame
					if old {
						return updateOldExpression{column}, true, nil
					}
					return column, true, nil
				}, graft)
				return output, true, err
			}
			if c.scope == 1 && c.frame == 1 && c.slot >= logicalWidth {
				c.slot += physicalWidth - logicalWidth
				return c, true, nil
			}
			return n, false, nil
		}, graft)
		return next, err
	}
	var err error
	if _, inserting := stmt.(*parser.Insert); !inserting {
		predicates := make([]BoundExpr, len(w.predicates))
		for i, x := range w.predicates {
			predicates[i], _, err = rewriteViewExpression(auth.catalog, x, func(n BoundExpr) (BoundExpr, bool, error) { return n, false, nil }, graft)
			if err != nil {
				return err
			}
		}
		w.predicates = predicates
	}
	for i := range w.checks {
		for j, x := range w.checks[i].predicates {
			w.checks[i].predicates[j], _, err = rewriteViewExpression(auth.catalog, x, func(n BoundExpr) (BoundExpr, bool, error) { return n, false, nil }, graft)
			if err != nil {
				return err
			}
		}
	}
	if b.update != nil {
		u := *b.update
		u.assignments = append([]BoundExpr(nil), u.assignments...)
		for i, x := range u.assignments {
			u.assignments[i], err = lower(x)
			if err != nil {
				return err
			}
		}
		if len(u.arrayTargets) != 0 {
			u.arrayTargets = append([]*arrayAssignmentTarget(nil), u.arrayTargets...)
			for i, target := range u.arrayTargets {
				if target == nil {
					continue
				}
				node, _, failure := mapSubscript(target.node, func(x BoundExpr) (BoundExpr, bool, error) { next, err := lower(x); return next, true, err })
				if failure != nil {
					return failure
				}
				copy := *target
				copy.node = node
				u.arrayTargets[i] = &copy
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
		r.expressions = append([]BoundExpr(nil), r.expressions...)
		for i, x := range r.expressions {
			r.expressions[i], err = lower(x)
			if err != nil {
				return err
			}
		}
		b.returning = &r
	}
	if b.conflict != nil {
		conflict := *b.conflict
		conflict.targets = b.targets
		conflict.keys = append([]conflictKey(nil), conflict.keys...)
		for i, key := range conflict.keys {
			if key.attribute != 0 {
				slot := -1
				for j, attr := range w.logical.relation.attributes {
					if attr == key.attribute {
						slot = j
						break
					}
				}
				if slot < 0 {
					return ErrCatalogReplaced
				}
				output, ok := viewDirectColumn(w.outputs[slot])
				if !ok {
					return sqlError("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification")
				}
				conflict.keys[i].attribute = w.base.attributes[output.slot]
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
		b.conflict = &conflict
	}
	if insert, ok := stmt.(*parser.Insert); ok {
		return e.lowerViewInsert(b, auth, insert)
	}
	if _, ok := stmt.(*parser.Update); ok {
		for _, slot := range b.targets {
			name := w.base.columns[slot].Name
			attr := w.base.attributes[slot]
			if _, generated := w.base.generations[attr]; generated {
				return generationWriteError(name, true)
			}
			if identity, ok := w.base.identities[attr]; ok && identity.mode == parser.IdentityAlways {
				d := identityInsertError(name).(*sqlstate.Error)
				d.Message = fmt.Sprintf("column %q can only be updated to DEFAULT", name)
				d.Hint = ""
				return d
			}
			for i, target := range b.targets {
				if target == slot && b.update.assignments[i] == nil {
					assignment := stmt.(*parser.Update).Assignments[i]
					return expressionError("0A000", "UPDATE DEFAULT is unsupported", assignment.Value.SourcePosition())
				}
			}
		}
	}
	return nil
}

// Required view quals precede user work, innermost first, including after a row
// wait. SQL UNKNOWN is rejected by the existing updateMatches TRUE-only rule.
func viewWritePredicate(predicates []BoundExpr, user BoundExpr) BoundExpr {
	if len(predicates) == 0 {
		return user
	}
	return viewQualificationExpression{expressionInfo: boundExpressionInfo(predicates[0]), predicates: predicates, user: user}
}
