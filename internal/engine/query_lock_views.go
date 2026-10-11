package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// View rowmarks share the existing arbitration and expanded physical occurrence
// manifest. Outer joins settle local input marks at source demand, before match
// filtering. The admitted top-level StatementScope.querySources stays unchanged.
type queryLockContext struct {
	leaves []queryLockLeaf
	// DECLARE-only physical manifests retain transparent lineage without
	// requesting the effective-version/provenance caches needed by rowmarks.
	// Ordinary initialized contexts remain semantic (the zero value).
	identityOnly bool
}

func (c *queryLockContext) rowMarked() bool {
	return c != nil && !c.identityOnly
}

type queryLockLeaf struct {
	leaf    queryLeaf
	source  *querySource
	mark    queryRowMark
	changed bool
	absent  bool
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
	state            *queryState
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
		case *limitNode:
			node = n.child
		case *evaluationNode:
			node = n.child
		case *rowLockNode:
			return n.firstMode
		case *distinctNode, *distinctOnNode, *aggregateNode:
			return 0 // Descendant marks are consumed at materialization.
		default:
			return 0
		}
	}
}
func (e *Engine) firstQueryRowModeLocked(scope *StatementScope, root LogicalNode, leaves []queryLeaf) (relationLockMode, error) {
	catalog := e.privateCatalogLocked(scope.owner.catalog)
	if _, err := validateViewReadGraph(catalog, leaves); err != nil {
		return 0, err
	}
	if mode := queryRootRowMode(root); mode != 0 {
		return mode, nil
	}
	var visit func(*relationDescriptor, queryRowMark, relationLockMode) (relationLockMode, error)
	visit = func(d *relationDescriptor, inherited queryRowMark, diagnostic relationLockMode) (relationLockMode, error) {
		if d.relationKind != viewRelation {
			if inherited.mode == 0 {
				return 0, nil
			}
			if diagnostic != 0 {
				return diagnostic, nil
			}
			return inherited.mode, nil
		}
		if inherited.mode != 0 {
			return queryRowDiagnostic(inherited), nil
		}
		if diagnostic == 0 {
			diagnostic = queryRootRowMode(d.view.query.Root)
		}
		for i, leaf := range d.view.sources {
			child, ok := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
			if !ok || leaf.ref.owner != e.owner || leaf.ref.Epoch != e.state.epoch {
				return 0, ErrCatalogReplaced
			}
			first, err := visit(child, viewLeafRowMark(d.view, i, inherited), diagnostic)
			if err != nil || first != 0 {
				return first, err
			}
		}
		return 0, nil
	}
	for _, leaf := range leaves {
		first, err := visit(leaf.relation, leaf.mark, queryRowDiagnostic(leaf.mark))
		if err != nil || first != 0 {
			return first, err
		}
	}
	return 0, nil
}
func viewHasStoredRowMarks(d *relationDescriptor, catalog CatalogRead) bool {
	seen := make(map[ObjectAddress]bool)
	var visit func(*relationDescriptor) bool
	visit = func(d *relationDescriptor) bool {
		if d.relationKind != viewRelation || seen[d.address] {
			return false
		}
		seen[d.address] = true
		if queryRootRowMode(d.view.query.Root) != 0 {
			return true
		}
		for _, leaf := range d.view.sources {
			if leaf.mark.mode != 0 {
				return true
			}
			if child, ok := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor); ok && visit(child) {
				return true
			}
		}
		return false
	}
	return visit(d)
}
func viewRowLockLegality(d *viewDefinition, mode relationLockMode) error {
	if queryHasSetOutput(d.query.Root) {
		return nil // A stored set view is an opaque physical-lineage boundary.
	}
	return queryRowLockLegality(d.query.Root, mode)
}

// Only the stored query's output boundary stops inherited reference-kind
// context. Ordinary joins/derived queries retain their existing legality and
// sibling checks; a nested stored set view applies this boundary on its entry.
func queryHasSetOutput(node LogicalNode) bool {
	for {
		switch n := node.(type) {
		case *projectNode:
			node = n.child
		case *sortNode:
			node = n.child
		case *limitNode:
			node = n.child
		case *evaluationNode:
			node = n.child
		case *filterNode:
			node = n.child
		case *setNode:
			return true
		default:
			return false
		}
	}
}

func queryRowLockLegality(root LogicalNode, mode relationLockMode) error {
	if mode == 0 {
		return nil
	}
	project, ok := root.(*projectNode)
	if !ok || project.syntax == nil {
		return fmt.Errorf("stuntgres: missing rowmark query syntax")
	}
	suffix := ""
	switch {
	case project.syntax.Set != nil:
		suffix = "UNION/INTERSECT/EXCEPT"
	case project.syntax.Distinct != nil:
		suffix = "DISTINCT clause"
	case project.syntax.GroupBy != nil:
		suffix = "GROUP BY clause"
	case project.syntax.Having != nil:
		suffix = "HAVING clause"
	default:
		for node := project.child; node != nil; {
			switch n := node.(type) {
			case *projectNode:
				node = n.child
			case *limitNode:
				node = n.child
			case *evaluationNode:
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
			case *windowNode:
				suffix = "window functions"
				node = nil
			case *distinctNode, *distinctOnNode:
				suffix = "DISTINCT clause"
				node = nil
			default:
				node = nil
			}
		}
	}
	if suffix != "" {
		return sqlError("0A000", queryRowClause(mode)+" is not allowed with "+suffix)
	}
	var from func(LogicalNode) error
	from = func(node LogicalNode) error {
		switch n := node.(type) {
		case *derivedNode:
			return queryRowLockLegality(n.query.Root, strongerQueryRowMode(mode, queryRootRowMode(n.query.Root)))
		case *joinNode:
			if err := from(n.left); err != nil {
				return err
			}
			return from(n.right)
		}
		return nil
	}
	return from(viewFromRoot(project))
}

// Called before any source expression is opened. Inherited rowmark legality,
// kind and authorization checks remain Execute-only.
func (e *Engine) initializeQueryLocksLocked(scope *StatementScope, root LogicalNode, leaves []queryLeaf) error {
	marked := queryRootRowMode(root) != 0
	catalog := e.privateCatalogLocked(scope.owner.catalog)
	visibilityDemand, err := validateViewReadGraph(catalog, leaves)
	if err != nil {
		return err
	}
	scope.visibilityDemand = visibilityDemand
	var visit func(*relationDescriptor, queryRowMark, bool) error
	visit = func(d *relationDescriptor, inherited queryRowMark, referenceMarked bool) error {
		if d.relationKind == viewRelation {
			if queryHasSetOutput(d.view.query.Root) {
				referenceMarked = false
			}
			referenceMarked = referenceMarked || queryRootRowMode(d.view.query.Root) != 0
			marked = marked || queryRootRowMode(d.view.query.Root) != 0
			if err := viewRowLockLegality(d.view, queryRowDiagnostic(inherited)); err != nil {
				return err
			}
			for i, leaf := range d.view.sources {
				child, ok := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
				if !ok || leaf.ref.owner != e.owner || leaf.ref.Epoch != e.state.epoch {
					return ErrCatalogReplaced
				}
				if err := visit(child, viewLeafRowMark(d.view, i, inherited), referenceMarked); err != nil {
					return err
				}
			}
			return nil
		}
		if (referenceMarked || inherited.mode != 0) && d.relationKind == materializedViewRelation {
			return sqlError("42809", fmt.Sprintf("cannot lock rows in materialized view %q", d.name))
		}
		// PostgreSQL validates reference rowmark kinds even for an unselected
		// sequence sibling. This does not confer UPDATE ACLs or tuple grants.
		if referenceMarked && d.relationKind == sequenceRelation {
			return sqlError("42809", fmt.Sprintf("cannot lock rows in sequence %q", d.name))
		}
		if inherited.mode == 0 {
			return nil
		}
		marked = true
		if d.relationKind == catalogRelation {
			return sqlError("0A000", "row locking is not supported for virtual catalog relations")
		}
		return nil
	}
	for _, leaf := range leaves {
		if err := visit(leaf.relation, leaf.mark, leaf.deleteReference || leaf.query != nil && len(leaf.query.Locking) != 0); err != nil {
			return err
		}
	}
	if marked {
		scope.queryLocks = &queryLockContext{}
	}
	return nil
}

func (v AuthorizationView) checkViewRowMarks(d *relationDescriptor, inherited queryRowMark, invoker PrincipalRef) error {
	if _, err := validateViewReadGraph(v.catalog, []queryLeaf{{relation: d}}); err != nil {
		return err
	}
	return v.checkViewRowMarksExpanded(d, inherited, invoker)
}

func (v AuthorizationView) checkViewRowMarksExpanded(d *relationDescriptor, inherited queryRowMark, invoker PrincipalRef) error {
	if d.relationKind != viewRelation {
		return nil
	}
	if err := viewRowLockLegality(d.view, queryRowDiagnostic(inherited)); err != nil {
		return err
	}
	principal := d.owner
	if d.view.invoker {
		principal = invoker
	}
	v.state.CurrentUser = principal
	for i, leaf := range d.view.sources {
		mark := viewLeafRowMark(d.view, i, inherited)
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
		if mark.mode != 0 {
			if err := v.checkColumns(binding{ref: leaf.ref, relation: child}, UpdatePrivilege, nil); err != nil {
				return err
			}
		}
		if err := v.checkViewRowMarksExpanded(child, mark, invoker); err != nil {
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
	case *limitNode:
		return queryLockRecheckWidth(n.child, inputs)
	case *aggregateNode, *windowNode, *distinctNode, *distinctOnNode, *setNode:
		return 0 // Its input marks/provenance have already been consumed.
	case *evaluationNode:
		return queryLockRecheckWidth(n.child, inputs)
	case *derivedNode:
		return queryLockRecheckWidth(n.query.Root, inputs)
	case *sourceNode:
		return queryLockSourceWidth(querySourceAt(inputs, n.occurrence))
	case *singletonNode, *valuesNode:
		return 0
	case *rowLockNode:
		return queryLockRecheckWidth(n.child, inputs)
	case *projectNode:
		if n.frozenValues {
			return 0
		}
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
	if source.lineage != nil && source.lineage.currentIndex == absentQueryIndex {
		return nil, true, nil
	}
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
		if !changedQueryLockSource(source.lineage.stage, evaluation.liveScope().queryLocks) {
			return source.row(source.lineage.currentIndex), true, nil
		}
		child := *evaluation
		lineage := source.lineage
		if lineage.state != nil {
			child.Frame = lineage.state.frame
			if child.Frame == 0 {
				child.Frame = 1
			}
			child.Outer, child.query = lineage.state.outer, lineage.state
			child.Parameters, child.Input, child.Output = lineage.state.parameters, lineage.state.input, lineage.state.output
		}
		child.orderValues = lineage.currentOrder
		frozenIndex := lineage.currentIndex
		if lineage.selection != nil {
			frozenIndex = lineage.selection[frozenIndex]
		}
		frozen := lineage.stage.row(frozenIndex)
		return recheckLockedQuery(lineage.root, lineage.inputs, rows, &child, scratch, offset, frozen)
	}
	return rows[source.occurrence], true, nil
}
func recheckLockedQuery(node LogicalNode, inputs *querySource, rows [][]sqltype.Value, evaluation *EvalContext, scratch []sqltype.Value, offset *int, frozen []sqltype.Value) ([]sqltype.Value, bool, error) {
	if absentLockedQuery(node, inputs, evaluation.liveScope().queryLocks) {
		return nil, true, nil
	}
	switch n := node.(type) {
	case *derivedNode:
		if evaluation.query == nil || evaluation.query.derived[n] == nil {
			return nil, false, fmt.Errorf("stuntgres: missing derived rowmark lineage")
		}
		return recheckLockedSource(evaluation.query.derived[n], rows, evaluation, scratch, offset)
	case *valuesNode:
		return frozen, true, nil
	case *distinctNode, *distinctOnNode, *setNode:
		return frozen, true, nil // Its source marks were settled during materialization.
	case *limitNode:
		return recheckLockedQuery(n.child, inputs, rows, evaluation, scratch, offset, frozen)
	case *evaluationNode:
		row, ok, err := recheckLockedQuery(n.child, inputs, rows, evaluation, scratch, offset, frozen)
		if err != nil || !ok {
			return nil, ok, err
		}
		evaluation.Row = row
		for i, expr := range n.expressions {
			value, err := expr.Eval(evaluation)
			if err != nil {
				return nil, false, err
			}
			evaluation.orderValues[n.base+i] = value
		}
		return row, true, nil
	case *sourceNode:
		return recheckLockedSource(querySourceAt(inputs, n.occurrence), rows, evaluation, scratch, offset)
	case *singletonNode:
		return nil, true, nil
	case *rowLockNode:
		return recheckLockedQuery(n.child, inputs, rows, evaluation, scratch, offset, frozen)
	case *sortNode:
		row, ok, err := recheckLockedQuery(n.child, inputs, rows, evaluation, scratch, offset, frozen)
		if err != nil || !ok {
			return nil, ok, err
		}
		if err := refreshLockedOrder(n, row, evaluation); err != nil {
			return nil, false, err
		}
		return row, true, nil
	case *filterNode:
		row, ok, err := recheckLockedQuery(n.child, inputs, rows, evaluation, scratch, offset, frozen)
		if err != nil || !ok {
			return nil, ok, err
		}
		evaluation.Row = row
		matches, err := updateMatches(evaluation, n.predicate)
		return row, matches, err
	case *projectNode:
		if n.frozenValues {
			return frozen, true, nil
		}
		row, ok, err := recheckLockedQuery(n.child, inputs, rows, evaluation, scratch, offset, frozen)
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
		left, ok, err := recheckLockedQuery(n.left, inputs, rows, evaluation, scratch, offset, frozen[:n.leftWidth])
		if err != nil || !ok {
			return nil, ok, err
		}
		right, ok, err := recheckLockedQuery(n.right, inputs, rows, evaluation, scratch, offset, frozen[n.leftWidth:n.leftWidth+n.rightWidth])
		if err != nil || !ok {
			return nil, ok, err
		}
		frame := scratch[*offset : *offset+len(n.Schema())]
		*offset += len(frame)
		leftAbsent := absentLockedQuery(n.left, inputs, evaluation.liveScope().queryLocks)
		rightAbsent := absentLockedQuery(n.right, inputs, evaluation.liveScope().queryLocks)
		if leftAbsent {
			for i := range n.leftWidth {
				frame[i] = sqltype.Null(n.Schema()[i].Usage.Type)
			}
		} else {
			copy(frame, left)
		}
		if rightAbsent {
			for i := n.leftWidth; i < n.leftWidth+n.rightWidth; i++ {
				frame[i] = sqltype.Null(n.Schema()[i].Usage.Type)
			}
		} else {
			copy(frame[n.leftWidth:], right)
		}
		evaluation.Row = frame
		if !leftAbsent && !rightAbsent {
			matches, err := updateMatches(evaluation, n.predicate)
			if err != nil || !matches {
				return nil, matches, err
			}
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
// consumes its provenance. Scalar view layers defer to their nearest owning
// frontier, including an outer join's local input producer.
func arbitrateViewQuery(ctx context.Context, state *queryState, node LogicalNode, child indexedStage) (indexedStage, error) {
	locks := state.source.locks
	due := make([]queryRowMark, len(locks.leaves))
	for _, mark := range child.pending {
		for _, occurrence := range mark.occurrences {
			due[occurrence] = mergeQueryRowMark(due[occurrence], mark.mark)
		}
	}
	for i, mark := range due {
		if mark.mode != 0 {
			due[i] = mergeQueryRowMark(mark, locks.leaves[i].mark)
		}
	}
	occurrences := appendQueryOccurrences(nil, child.source)
	provenance := make([]int, 0, len(occurrences))
	versions := make([]rowVersion, 0, len(occurrences))
	currentRows := make([][]sqltype.Value, len(locks.leaves))
	scratch := make([]sqltype.Value, queryLockRecheckWidth(node, state.source))
	evaluation := state.evaluation(nil)
	joined := child.source.materialized
	source := child.source
	if !joined {
		source = &querySource{anonymous: [][]sqltype.Value{}, materialized: true, occurrences: occurrences, authority: state.source.authority, scope: state.source.scope}
	}
	source.occurrences = occurrences
	out := producingStage(&child, child.schema)
	out.source, out.expressions, out.evaluation = source, child.expressions, child.evaluation
	out.pending = nil
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		scope := state.source.liveScope()
		if scope == nil {
			return false, fmt.Errorf("stuntgres: missing row locking scope")
		}
		for {
			i, present, err := child.nextOccurrence(ctx)
			if err != nil || !present {
				return present, err
			}
			evaluation.setContext(ctx)
			index := child.index(i)
			setQueryLineage(child.source, index)
			provenance = appendQueryProvenance(provenance[:0], child.source, index)
			versions = appendQueryVersions(versions[:0], child.source, index, locks)
			live, changed := true, false
			for j, occurrence := range occurrences {
				entry := locks.leaves[occurrence]
				visible := provenance[j]
				locks.leaves[occurrence].absent = visible == absentQueryIndex
				if visible == absentQueryIndex {
					currentRows[occurrence] = nil
					locks.leaves[occurrence].changed = false
					continue
				}
				mark := due[occurrence]
				if mark.mode == 0 {
					currentRows[occurrence] = versions[j].values
					locks.leaves[occurrence].changed = false
					continue
				}
				raw := entry.source.rawIndex(visible)
				candidate := versions[j]
				hint := raw
				if raw >= len(entry.source.committed) {
					hint -= len(entry.source.committed)
				}
				acquired, err := scope.acquireQueryRow(entry.leaf, candidate.row, mark)
				if err != nil {
					return false, err
				}
				if !acquired {
					live = false
					break
				}
				current, present, err := scope.latestQueryRow(entry.leaf, candidate, hint, mark.mode)
				if err != nil {
					return false, err
				}
				live = live && present
				changed = changed || current.version != candidate.version
				currentRows[occurrence] = current.values
				versions[j] = current
				locks.leaves[occurrence].changed = current.version != candidate.version
			}
			if !live {
				continue
			}
			row := child.source.row(index)
			if changed {
				evaluation.orderValues = child.orderRow(i)
				offset := 0
				replacement, ok, err := recheckLockedQuery(node, state.source, currentRows, &evaluation, scratch, &offset, row)
				if err != nil {
					return false, err
				}
				if !ok {
					continue
				}
				row = replacement
			}
			if joined {
				if changed {
					copy(child.source.row(index), row)
				}
				width := len(occurrences)
				if width > 0 {
					if missing := (index+1)*width - len(source.versions); missing > 0 {
						if source.versionValid == nil {
							source.versionValid = make([]bool, len(source.versions)/width)
							for i := range source.versionValid {
								source.versionValid[i] = true
							}
						}
						source.versionValid = append(source.versionValid, make([]bool, index+1-len(source.versionValid))...)
						source.versions = append(source.versions, make([]rowVersion, missing)...)
					}
					copy(source.versions[index*width:(index+1)*width], versions)
					if source.versionValid != nil {
						source.versionValid[index] = true
					}
				}
				out.selection = append(out.selection, index)
			} else {
				if changed || child.source.input != nil || child.source.final != nil && child.source.final.source != nil {
					row = append([]sqltype.Value(nil), row...)
				}
				out.selection = append(out.selection, len(source.anonymous))
				source.anonymous = append(source.anonymous, row)
				source.provenance = append(source.provenance, provenance...)
				source.versions = append(source.versions, versions...)
				source.flatCount++
			}
			out.cacheWidth, out.orderValues = child.cacheWidth, child.orderValues
			if child.cacheWidth != 0 {
				out.cacheOccurrences = append(out.cacheOccurrences, child.orderOccurrence(i))
			}
			return true, ctx.Err()
		}
	}
	return out, nil
}

// Inherited marks follow physical lineage, unlike general source/dependency
// traversal: a materialized set still reads both arms but exposes neither's rows.
func queryRowMarkReachesSource(node LogicalNode, occurrence int) bool {
	if node == nil {
		return false
	}
	if _, ok := node.(*setNode); ok {
		return false
	}
	if source, ok := node.(*sourceNode); ok {
		return source.occurrence == occurrence
	}
	left, right := queryInputs(node)
	return queryRowMarkReachesSource(left, occurrence) || queryRowMarkReachesSource(right, occurrence)
}

func viewLeafRowMark(view *viewDefinition, occurrence int, inherited queryRowMark) queryRowMark {
	mark := view.sources[occurrence].mark
	if inherited.mode != 0 && queryRowMarkReachesSource(view.query.Root, occurrence) {
		mark = mergeQueryRowMark(inherited, mark)
	}
	return mark
}

func queryHasPhysicalInput(node LogicalNode) bool {
	if node == nil {
		return false
	}
	if _, ok := node.(*sourceNode); ok {
		return true
	}
	left, right := queryInputs(node)
	return queryHasPhysicalInput(left) || queryHasPhysicalInput(right)
}
