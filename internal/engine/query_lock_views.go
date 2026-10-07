package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// View rowmarks share the enclosing query's frontier and physical occurrence
// manifest. The admitted top-level StatementScope.querySources stays unchanged.
type queryLockContext struct {
	leaves []queryLockLeaf
}
type queryLockLeaf struct {
	leaf    queryLeaf
	source  *querySource
	mode    relationLockMode
	changed bool
}
type queryLockLineage struct {
	root             LogicalNode
	inputs, stage    *querySource
	selection        []int
	orderValues      []sqltype.Value
	cacheWidth       int
	cacheOccurrences []int
	currentOrder     []sqltype.Value
	currentIndex     int
}

func strongerQueryRowMode(a, b relationLockMode) relationLockMode {
	for _, mode := range [...]relationLockMode{rowUpdate, noKeyUpdate, rowShare, keyShare} {
		if a == mode || b == mode {
			return mode
		}
	}
	return 0
}
func queryRowClause(mode relationLockMode) string {
	switch mode {
	case keyShare:
		return "FOR KEY SHARE"
	case rowShare:
		return "FOR SHARE"
	case noKeyUpdate:
		return "FOR NO KEY UPDATE"
	case rowUpdate:
		return "FOR UPDATE"
	}
	return ""
}
func queryRootRowMode(node LogicalNode) relationLockMode {
	for {
		switch n := node.(type) {
		case *projectNode:
			node = n.child
		case *rowLockNode:
			return n.mode
		default:
			return 0
		}
	}
}
func queryOwnAggregate(node LogicalNode) bool {
	for {
		switch n := node.(type) {
		case *projectNode:
			node = n.child
		case *filterNode:
			node = n.child
		case *sortNode:
			node = n.child
		case *rowLockNode:
			node = n.child
		case *aggregateNode:
			return true
		default:
			return false
		}
	}
}
func (e *Engine) firstQueryRowModeLocked(scope *StatementScope, root LogicalNode, leaves []queryLeaf) (relationLockMode, error) {
	catalog := e.sequenceCatalogLocked(scope.owner.sequences)
	mode := queryRootRowMode(root)
	var visit func(*relationDescriptor, relationLockMode) (relationLockMode, error)
	visit = func(d *relationDescriptor, mode relationLockMode) (relationLockMode, error) {
		if d.view == nil {
			return mode, nil
		}
		mode = viewRowMode(d.view, mode)
		for _, leaf := range d.view.sources {
			child, ok := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
			if !ok || leaf.ref.owner != e.owner || leaf.ref.Epoch != e.state.epoch {
				return 0, ErrCatalogReplaced
			}
			first, err := visit(child, mode)
			if err != nil || first != 0 {
				return first, err
			}
		}
		return 0, nil
	}
	for _, leaf := range leaves {
		first, err := visit(leaf.relation, mode)
		if err != nil || first != 0 {
			return first, err
		}
	}
	return 0, nil
}
func validateQueryViewRowMarks(leaves []queryLeaf, mode relationLockMode, catalog CatalogRead) error {
	var visit func(*relationDescriptor, relationLockMode) error
	visit = func(d *relationDescriptor, inherited relationLockMode) error {
		if d.view == nil {
			return nil
		}
		inherited = viewRowMode(d.view, inherited)
		if err := viewRowLockLegality(d.view, inherited); err != nil {
			return err
		}
		for _, leaf := range d.view.sources {
			child, ok := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
			if !ok || leaf.ref.owner != catalog.owner.owner || leaf.ref.Epoch != catalog.Epoch() {
				return ErrCatalogReplaced
			}
			if err := visit(child, inherited); err != nil {
				return err
			}
		}
		return nil
	}
	for _, leaf := range leaves {
		if err := visit(leaf.relation, mode); err != nil {
			return err
		}
	}
	return nil
}
func viewRowMode(d *viewDefinition, inherited relationLockMode) relationLockMode {
	return strongerQueryRowMode(inherited, queryRootRowMode(d.query.Root))
}
func viewHasStoredRowMarks(d *relationDescriptor, catalog CatalogRead) bool {
	if d.view == nil {
		return false
	}
	if queryRootRowMode(d.view.query.Root) != 0 {
		return true
	}
	for _, leaf := range d.view.sources {
		if child, ok := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor); ok && viewHasStoredRowMarks(child, catalog) {
			return true
		}
	}
	return false
}
func viewRowLockLegality(d *viewDefinition, mode relationLockMode) error {
	if mode == 0 {
		return nil
	}
	suffix := ""
	switch {
	case len(d.syntax.GroupBy) != 0:
		suffix = "GROUP BY clause"
	case d.syntax.Having != nil:
		suffix = "HAVING clause"
	default:
		node := d.query.Root
		for node != nil {
			switch n := node.(type) {
			case *projectNode:
				node = n.child
			case *sortNode:
				node = n.child
			case *filterNode:
				node = n.child
			case *rowLockNode:
				node = n.child
			case *aggregateNode:
				suffix = "aggregate functions"
				node = nil
			default:
				node = nil
			}
		}
	}
	if suffix != "" {
		return sqlError("0A000", queryRowClause(mode)+" is not allowed with "+suffix)
	}
	return nil
}

// Called before any source expression is opened. Kind and authorization checks
// remain Execute-only; Describe checks structural expanded-view grouping.
func (e *Engine) initializeQueryLocksLocked(scope *StatementScope, root LogicalNode, leaves []queryLeaf) error {
	inherited := queryRootRowMode(root)
	marked, views := false, false
	catalog := e.sequenceCatalogLocked(scope.owner.sequences)
	var visit func(*relationDescriptor, relationLockMode) error
	visit = func(d *relationDescriptor, mode relationLockMode) error {
		if d.view != nil {
			views = true
			mode = viewRowMode(d.view, mode)
			marked = marked || mode != 0
			if err := viewRowLockLegality(d.view, mode); err != nil {
				return err
			}
			for _, leaf := range d.view.sources {
				child, ok := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
				if !ok || leaf.ref.owner != e.owner || leaf.ref.Epoch != e.state.epoch {
					return ErrCatalogReplaced
				}
				if err := visit(child, mode); err != nil {
					return err
				}
			}
			return nil
		}
		if mode == 0 {
			return nil
		}
		marked = true
		switch d.relationKind {
		case catalogRelation:
			return sqlError("0A000", "row locking is not supported for virtual catalog relations")
		case sequenceRelation:
			return sqlError("42809", fmt.Sprintf("cannot lock rows in sequence %q", d.name))
		}
		return nil
	}
	for _, leaf := range leaves {
		if err := visit(leaf.relation, inherited); err != nil {
			return err
		}
	}
	if marked && views {
		scope.queryLocks = &queryLockContext{}
	}
	return nil
}

func (v AuthorizationView) checkViewRowMarks(d *relationDescriptor, inherited relationLockMode, invoker PrincipalRef) error {
	if d.view == nil {
		return nil
	}
	mode := viewRowMode(d.view, inherited)
	if err := viewRowLockLegality(d.view, mode); err != nil {
		return err
	}
	principal := d.owner
	if d.view.invoker {
		principal = invoker
	}
	v.state.CurrentUser = principal
	for _, leaf := range d.view.sources {
		child, ok := v.catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
		if !ok || leaf.ref.owner != v.catalog.owner.owner || leaf.ref.Epoch != v.catalog.Epoch() {
			return ErrCatalogReplaced
		}
		indices := make([]int, 0, len(leaf.reads))
		for _, attribute := range leaf.reads {
			found := false
			for slot, live := range child.attributes {
				if attribute == live {
					indices = append(indices, slot)
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("stuntgres: missing view source attribute")
			}
		}
		if err := v.checkColumns(binding{ref: leaf.ref, relation: child}, SelectPrivilege, indices); err != nil {
			return err
		}
		if mode != 0 {
			if err := v.checkColumns(binding{ref: leaf.ref, relation: child}, UpdatePrivilege, nil); err != nil {
				return err
			}
		}
		if err := v.checkViewRowMarks(child, mode, invoker); err != nil {
			return err
		}
	}
	return nil
}

func querySourceAt(bundle *querySource, occurrence int) *querySource {
	if bundle.sources != nil {
		return bundle.sources[occurrence]
	}
	return bundle
}
func queryLockRecheckWidth(node LogicalNode, inputs *querySource) int {
	switch n := node.(type) {
	case *sourceNode:
		return queryLockSourceWidth(querySourceAt(inputs, n.occurrence))
	case *singletonNode:
		return 0
	case *rowLockNode:
		return queryLockRecheckWidth(n.child, inputs)
	case *projectNode:
		return len(n.Schema()) + queryLockRecheckWidth(n.child, inputs)
	case *joinNode:
		return len(n.Schema()) + queryLockRecheckWidth(n.left, inputs) + queryLockRecheckWidth(n.right, inputs)
	case *filterNode:
		return queryLockRecheckWidth(n.child, inputs)
	case *sortNode:
		return queryLockRecheckWidth(n.child, inputs)
	}
	panic("invalid marked view recheck tree")
}
func queryLockSourceWidth(source *querySource) int {
	if source.input != nil {
		return len(source.layout) + queryLockSourceWidth(source.input)
	}
	if source.lineage != nil {
		return queryLockRecheckWidth(source.lineage.root, source.lineage.inputs)
	}
	return 0
}
func recheckLockedSource(source *querySource, rows [][]sqltype.Value, evaluation *EvalContext, scratch []sqltype.Value, offset *int) ([]sqltype.Value, bool, error) {
	if source.input != nil {
		row, ok, err := recheckLockedSource(source.input, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		frame := scratch[*offset : *offset+len(source.layout)]
		*offset += len(frame)
		for i, slot := range source.layout {
			if slot >= 0 {
				frame[i] = row[slot]
			}
		}
		return frame, true, nil
	}
	if source.lineage != nil {
		if !changedQueryLockSource(source.lineage.stage, evaluation.statementScope.queryLocks) {
			return source.row(source.lineage.currentIndex), true, nil
		}
		previous := evaluation.orderValues
		evaluation.orderValues = source.lineage.currentOrder
		row, ok, err := recheckLockedQuery(source.lineage.root, source.lineage.inputs, rows, evaluation, scratch, offset)
		evaluation.orderValues = previous
		return row, ok, err
	}
	return rows[source.occurrence], true, nil
}
func recheckLockedQuery(node LogicalNode, inputs *querySource, rows [][]sqltype.Value, evaluation *EvalContext, scratch []sqltype.Value, offset *int) ([]sqltype.Value, bool, error) {
	switch n := node.(type) {
	case *sourceNode:
		return recheckLockedSource(querySourceAt(inputs, n.occurrence), rows, evaluation, scratch, offset)
	case *singletonNode:
		return nil, true, nil
	case *rowLockNode:
		return recheckLockedQuery(n.child, inputs, rows, evaluation, scratch, offset)
	case *sortNode:
		row, ok, err := recheckLockedQuery(n.child, inputs, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		if err := refreshLockedOrder(n, row, evaluation); err != nil {
			return nil, false, err
		}
		return row, true, nil
	case *filterNode:
		row, ok, err := recheckLockedQuery(n.child, inputs, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		evaluation.Row = row
		matches, err := updateMatches(evaluation, n.predicate)
		return row, matches, err
	case *projectNode:
		row, ok, err := recheckLockedQuery(n.child, inputs, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		frame := scratch[*offset : *offset+len(n.Schema())]
		*offset += len(frame)
		evaluation.Row = row
		for i, expr := range n.expressions {
			value, err := expr.Eval(evaluation)
			if err != nil {
				return nil, false, err
			}
			frame[i] = value
		}
		return frame, true, nil
	case *joinNode:
		left, ok, err := recheckLockedQuery(n.left, inputs, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		right, ok, err := recheckLockedQuery(n.right, inputs, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		frame := scratch[*offset : *offset+len(n.Schema())]
		*offset += len(frame)
		copy(frame, left)
		copy(frame[n.leftWidth:], right)
		evaluation.Row = frame
		matches, err := updateMatches(evaluation, n.predicate)
		if err != nil || !matches {
			return nil, matches, err
		}
		for i, expr := range n.merges {
			value, err := expr.Eval(evaluation)
			if err != nil {
				return nil, false, err
			}
			frame[n.leftWidth+n.rightWidth+i] = value
		}
		return frame, true, nil
	}
	return nil, false, fmt.Errorf("stuntgres: invalid marked view recheck tree")
}

// Arbitrate the qualified physical input before projection or aggregation
// consumes its provenance. Scalar view layers defer to their enclosing frontier.
func arbitrateViewQuery(ctx context.Context, state *queryState, node LogicalNode, child indexedStage) (indexedStage, error) {
	locks := state.source.locks
	scope := state.source.scope
	occurrences := appendQueryOccurrences(nil, child.source)
	provenance := make([]int, 0, len(occurrences))
	currentRows := make([][]sqltype.Value, len(locks.leaves))
	scratch := make([]sqltype.Value, queryLockRecheckWidth(node, state.source))
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Parameters: state.parameters, statementScope: scope, Input: state.input, Output: state.output}
	joined := child.source.materialized
	out := child.source
	var selection []int
	var cacheOccurrences []int
	if child.cacheWidth != 0 {
		cacheOccurrences = make([]int, 0, child.count())
	}
	if joined {
		selection = make([]int, 0, child.count())
	} else {
		out = &querySource{anonymous: make([][]sqltype.Value, 0, child.count())}
	}
	for i := range child.count() {
		if err := ctx.Err(); err != nil {
			return indexedStage{}, err
		}
		index := child.index(i)
		provenance = appendQueryProvenance(provenance[:0], child.source, index)
		live, changed := true, false
		for j, occurrence := range occurrences {
			entry := locks.leaves[occurrence]
			visible := provenance[j]
			if entry.mode == 0 {
				currentRows[occurrence] = entry.source.row(visible)
				locks.leaves[occurrence].changed = false
				continue
			}
			raw := entry.source.rawIndex(visible)
			candidate := entry.source.versionAt(raw)
			hint := raw
			if raw >= len(entry.source.committed) {
				hint -= len(entry.source.committed)
			}
			if err := scope.acquireQueryRow(entry.leaf, candidate.row, entry.mode); err != nil {
				return indexedStage{}, err
			}
			current, present, err := scope.latestQueryRow(entry.leaf, candidate, hint, entry.mode)
			if err != nil {
				return indexedStage{}, err
			}
			live = live && present
			changed = changed || current.version != candidate.version
			currentRows[occurrence] = current.values
			locks.leaves[occurrence].changed = current.version != candidate.version
		}
		if !live {
			continue
		}
		row := child.source.row(index)
		if changed {
			evaluation.orderValues = child.orderRow(i)
			offset := 0
			replacement, ok, err := recheckLockedQuery(node, state.source, currentRows, &evaluation, scratch, &offset)
			if err != nil {
				return indexedStage{}, err
			}
			if !ok {
				continue
			}
			row = replacement
		}
		if child.cacheWidth != 0 {
			cacheOccurrences = append(cacheOccurrences, child.orderOccurrence(i))
		}
		if joined {
			if changed {
				copy(child.source.row(index), row)
			}
			selection = append(selection, index)
		} else {
			if changed || child.source.input != nil || child.source.final != nil && child.source.final.source != nil {
				// Only scratch-backed rows need an owned copy.
				row = append([]sqltype.Value(nil), row...)
			}
			out.anonymous = append(out.anonymous, row)
		}
	}
	return indexedStage{source: out, selection: selection, schema: child.schema, expressions: child.expressions, evaluation: child.evaluation, orderValues: child.orderValues, cacheWidth: child.cacheWidth, cacheOccurrences: cacheOccurrences}, nil
}
