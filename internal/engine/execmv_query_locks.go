package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
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
	mark    plan.RowMark
	changed bool
	absent  bool
}

type queryLockLineage struct {
	root             plan.Node
	inputs, stage    *querySource
	selection        []int
	orderValues      []sqltype.Value
	cacheWidth       int
	cacheOccurrences []int
	currentOrder     []sqltype.Value
	currentIndex     int
	state            *queryState
}

func strongerQueryRowMode(a, b lock.Mode) lock.Mode {
	for _, mode := range [...]lock.Mode{lock.ForUpdate, lock.ForNoKeyUpdate, lock.ForShare, lock.ForKeyShare} {
		if a == mode || b == mode {
			return mode
		}
	}
	return 0
}

func querySourceAt(bundle *querySource, occurrence int) *querySource {
	if bundle.sources != nil {
		return bundle.sources[occurrence]
	}
	return bundle
}

func queryLockRecheckWidth(node plan.Node, inputs *querySource) int {
	switch n := node.(type) {
	case *plan.FunctionFromNode:
		return len(n.Schema())
	case *plan.SetProjectNode:
		return 0 // Legal nonphysical inherited paths need no substitution scratch.
	case *plan.LimitNode:
		return queryLockRecheckWidth(n.Child, inputs)
	case *plan.AggregateNode, *plan.WindowNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.SetNode, *plan.RecursiveUnionNode, *plan.WorktableScanNode:
		return 0 // Its input marks/provenance have already been consumed.
	case *plan.EvaluationNode:
		return queryLockRecheckWidth(n.Child, inputs)
	case *plan.DerivedNode:
		if n.CTE != nil {
			return 0
		}
		return queryLockRecheckWidth(n.Query.Root, inputs)
	case *plan.SourceNode:
		return queryLockSourceWidth(querySourceAt(inputs, n.Occurrence))
	case *plan.SingletonNode, *plan.ValuesNode:
		return 0
	case *plan.RowLockNode:
		return queryLockRecheckWidth(n.Child, inputs)
	case *plan.ProjectNode:
		if n.FrozenValues {
			return 0
		}
		return len(n.Schema()) + queryLockRecheckWidth(n.Child, inputs)
	case *plan.JoinNode:
		return len(n.Schema()) + queryLockRecheckWidth(n.Left, inputs) + queryLockRecheckWidth(n.Right, inputs)
	case *plan.FilterNode:
		return queryLockRecheckWidth(n.Child, inputs)
	case *plan.SortNode:
		return queryLockRecheckWidth(n.Child, inputs)
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
		if !changedQueryLockSource(source.lineage.stage, evaluation.liveHost().QueryLocks()) {
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
		child.bindQueryIdentity(lineage.stage, frozenIndex)
		return recheckLockedQuery(lineage.root, lineage.inputs, rows, &child, scratch, offset, frozen)
	}
	return rows[source.occurrence], true, nil
}

func recheckLockedQuery(node plan.Node, inputs *querySource, rows [][]sqltype.Value, evaluation *EvalContext, scratch []sqltype.Value, offset *int, frozen []sqltype.Value) ([]sqltype.Value, bool, error) {
	if absentLockedQuery(node, inputs, evaluation.liveHost().QueryLocks()) {
		return nil, true, nil
	}
	switch n := node.(type) {
	case *plan.DerivedNode:
		if n.CTE != nil {
			return frozen, true, nil
		}
		if evaluation.query == nil || evaluation.query.derived[n] == nil {
			return nil, false, fmt.Errorf("stuntgres: missing derived rowmark lineage")
		}
		return recheckLockedSource(evaluation.query.derived[n], rows, evaluation, scratch, offset)
	case *plan.ValuesNode:
		return frozen, true, nil
	case *plan.FunctionFromNode:
		// PostgreSQL 18.6 keeps the function-scan tuple a row was produced
		// with; the recheck refreshes only locked rows and requalifies.
		return frozen, true, nil
	case *plan.DistinctNode, *plan.DistinctOnNode, *plan.SetNode, *plan.RecursiveUnionNode, *plan.WorktableScanNode:
		return frozen, true, nil // Its source marks were settled during materialization.
	case *plan.LimitNode:
		return recheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
	case *plan.EvaluationNode:
		row, ok, err := recheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
		if err != nil || !ok {
			return nil, ok, err
		}
		evaluation.Row = row
		for i, expr := range n.Expressions {
			value, err := expr.Eval(evaluation)
			if err != nil {
				return nil, false, err
			}
			evaluation.orderValues[n.Base+i] = value
		}
		return row, true, nil
	case *plan.SourceNode:
		return recheckLockedSource(querySourceAt(inputs, n.Occurrence), rows, evaluation, scratch, offset)
	case *plan.SingletonNode:
		return nil, true, nil
	case *plan.RowLockNode:
		return recheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
	case *plan.SortNode:
		row, ok, err := recheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
		if err != nil || !ok {
			return nil, ok, err
		}
		if err := refreshLockedOrder(n, row, evaluation); err != nil {
			return nil, false, err
		}
		return row, true, nil
	case *plan.FilterNode:
		row, ok, err := recheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
		if err != nil || !ok {
			return nil, ok, err
		}
		evaluation.Row = row
		matches, err := updateMatches(evaluation, n.Predicate)
		return row, matches, err
	case *plan.ProjectNode:
		if n.FrozenValues {
			return frozen, true, nil
		}
		row, ok, err := recheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
		if err != nil || !ok {
			return nil, ok, err
		}
		frame := scratch[*offset : *offset+len(n.Schema())]
		*offset += len(frame)
		evaluation.Row = row
		for i, expr := range n.Expressions {
			value, err := expr.Eval(evaluation)
			if err != nil {
				return nil, false, err
			}
			frame[i] = value
		}
		return frame, true, nil
	case *plan.JoinNode:
		left, ok, err := recheckLockedQuery(n.Left, inputs, rows, evaluation, scratch, offset, frozen[:n.LeftWidth])
		if err != nil || !ok {
			return nil, ok, err
		}
		right, ok, err := recheckLockedQuery(n.Right, inputs, rows, evaluation, scratch, offset, frozen[n.LeftWidth:n.LeftWidth+n.RightWidth])
		if err != nil || !ok {
			return nil, ok, err
		}
		frame := scratch[*offset : *offset+len(n.Schema())]
		*offset += len(frame)
		leftAbsent := absentLockedQuery(n.Left, inputs, evaluation.liveHost().QueryLocks())
		rightAbsent := absentLockedQuery(n.Right, inputs, evaluation.liveHost().QueryLocks())
		if leftAbsent {
			for i := range n.LeftWidth {
				frame[i] = sqltype.Null(n.Schema()[i].Usage.Type)
			}
		} else {
			copy(frame, left)
		}
		if rightAbsent {
			for i := n.LeftWidth; i < n.LeftWidth+n.RightWidth; i++ {
				frame[i] = sqltype.Null(n.Schema()[i].Usage.Type)
			}
		} else {
			copy(frame[n.LeftWidth:], right)
		}
		evaluation.Row = frame
		if !leftAbsent && !rightAbsent {
			matches, err := updateMatches(evaluation, n.Predicate)
			if err != nil || !matches {
				return nil, matches, err
			}
		}
		for i, expr := range n.Merges {
			value, err := expr.Eval(evaluation)
			if err != nil {
				return nil, false, err
			}
			frame[n.LeftWidth+n.RightWidth+i] = value
		}
		return frame, true, nil
	}
	return nil, false, fmt.Errorf("stuntgres: invalid marked view recheck tree")
}

// Arbitrate the qualified physical input before projection or aggregation
// consumes its provenance. Scalar view layers defer to their nearest owning
// frontier, including an outer join's local input producer.
func arbitrateViewQuery(ctx context.Context, state *queryState, node plan.Node, child indexedStage) (indexedStage, error) {
	locks := state.source.locks
	due := make([]plan.RowMark, len(locks.leaves))
	for _, mark := range child.pending {
		for _, occurrence := range mark.occurrences {
			due[occurrence] = mergeQueryRowMark(due[occurrence], mark.mark)
		}
	}
	for i, mark := range due {
		if mark.Mode != 0 {
			due[i] = mergeQueryRowMark(mark, locks.leaves[i].mark)
		}
	}
	occurrences := appendQueryOccurrences(nil, child.source)
	provenance := make([]int, 0, len(occurrences))
	versions := make([]storage.RowVersion, 0, len(occurrences))
	currentRows := make([][]sqltype.Value, len(locks.leaves))
	scratch := make([]sqltype.Value, queryLockRecheckWidth(node, state.source))
	evaluation := state.evaluation(nil)
	joined := child.source.materialized
	source := child.source
	if !joined {
		source = &querySource{anonymous: [][]sqltype.Value{}, materialized: true, occurrences: occurrences, authority: state.source.authority, host: state.source.host}
		source.presenceSlots, source.presenceCount = child.source.presenceSlots, child.source.presenceCount
		source.presenceConstant = child.source.presenceConstant
	}
	source.occurrences = occurrences
	out := producingStage(&child, child.schema)
	out.source, out.expressions, out.evaluation = source, child.expressions, child.evaluation
	out.pending = nil
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		host := state.source.liveHost()
		if host == nil {
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
			evaluation.queryOccurrences, evaluation.queryProvenance = occurrences, provenance
			versions = appendQueryVersions(versions[:0], child.source, index, locks)
			// Load the frozen frame before mapping latest physical values. A
			// nonidentity inheritance segment reuses its capture-owned scratch;
			// loading it afterward would overwrite currentRows with old values.
			row := child.source.row(index)
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
				locator := entry.source.physicalRow(visible, entry.leaf, versions[j])
				if mark.Mode == 0 {
					currentRows[occurrence] = locator.values(versions[j])
					locks.leaves[occurrence].changed = false
					continue
				}
				candidate := locator.version
				acquired, err := host.AcquireQueryRow(locator.leaf, candidate.Row, mark)
				if err != nil {
					return false, err
				}
				if !acquired {
					live = false
					break
				}
				current, present, err := host.LatestQueryRow(locator.leaf, candidate, locator.hint, mark.Mode)
				if err != nil {
					return false, err
				}
				live = live && present
				changed = changed || current.Version != candidate.Version
				currentRows[occurrence] = locator.values(current)
				versions[j] = current
				locks.leaves[occurrence].changed = current.Version != candidate.Version
			}
			if !live {
				continue
			}
			evaluation.setQueryPresence(child.source, index)
			if changed {
				evaluation.orderValues = child.orderRow(i)
				offset := 0
				previousRows := evaluation.queryCurrentRows
				evaluation.queryCurrentRows = currentRows
				replacement, ok, err := recheckLockedQuery(node, state.source, currentRows, &evaluation, scratch, &offset, row)
				evaluation.queryCurrentRows = previousRows
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
						source.versions = append(source.versions, make([]storage.RowVersion, missing)...)
					}
					copy(source.versions[index*width:(index+1)*width], versions)
					if source.versionValid != nil {
						source.versionValid[index] = true
					}
				}
				out.selection = append(out.selection, index)
			} else {
				mapped := false
				if child.source.inheritance != nil {
					segment, _ := child.source.inheritanceSegment(index)
					mapped = segment.layout != nil
				}
				if changed || mapped || child.source.input != nil || child.source.final != nil && child.source.final.source != nil {
					row = append([]sqltype.Value(nil), row...)
				}
				out.selection = append(out.selection, len(source.anonymous))
				source.anonymous = append(source.anonymous, row)
				if source.presenceCount != 0 && len(source.presenceConstant) == 0 {
					source.presenceFlat = append(source.presenceFlat, child.source.presenceRow(index)...)
				}
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

func queryHasPhysicalInput(node plan.Node) bool {
	if node == nil {
		return false
	}
	if _, ok := node.(*plan.SourceNode); ok {
		return true
	}
	left, right := plan.QueryInputs(node)
	return queryHasPhysicalInput(left) || queryHasPhysicalInput(right)
}

// absentQueryIndex is an absent physical occurrence, never a real NULL-valued row.
const absentQueryIndex = -1

// Input adapters translate row identity exactly as they translate row cells.
func (source *querySource) inputIndex(index int) int {
	if index != absentQueryIndex && source.windowIndices != nil {
		return source.windowIndices[index]
	}
	return index
}

func queryOccurrenceWidth(source *querySource) int {
	if source.input != nil {
		return queryOccurrenceWidth(source.input)
	}
	if source.lineage != nil {
		return queryOccurrenceWidth(source.lineage.stage)
	}
	if source.singleton {
		return 0
	}
	if source.materialized {
		return len(source.occurrences)
	}
	return 1
}

func appendQueryOccurrences(dst []int, source *querySource) []int {
	if source.input != nil {
		return appendQueryOccurrences(dst, source.input)
	}
	if source.lineage != nil {
		return appendQueryOccurrences(dst, source.lineage.stage)
	}
	if source.singleton {
		return dst
	}
	if source.materialized {
		return append(dst, source.occurrences...)
	}
	return append(dst, source.occurrence)
}

func appendQueryProvenance(dst []int, source *querySource, index int) []int {
	if index == absentQueryIndex {
		setQueryLineage(source, index)
		for range queryOccurrenceWidth(source) {
			dst = append(dst, absentQueryIndex)
		}
		return dst
	}
	if source.input != nil {
		return appendQueryProvenance(dst, source.input, source.inputIndex(index))
	}
	if source.lineage != nil {
		lineage := source.lineage
		lineage.currentIndex = index
		if lineage.cacheWidth != 0 {
			occurrence := index
			if lineage.cacheOccurrences != nil {
				occurrence = lineage.cacheOccurrences[index]
			}
			lineage.currentOrder = lineage.orderValues[occurrence*lineage.cacheWidth : (occurrence+1)*lineage.cacheWidth]
		}
		if source.lineage.selection != nil {
			index = source.lineage.selection[index]
		}
		return appendQueryProvenance(dst, source.lineage.stage, index)
	}
	if source.singleton {
		return dst
	}
	if source.materialized {
		width := len(source.occurrences)
		return append(dst, source.provenance[index*width:(index+1)*width]...)
	}
	return append(dst, index)
}

func openRowLockNode(ctx context.Context, state *queryState, n *plan.RowLockNode) (indexedStage, error) {
	if state.source.liveHost() == nil {
		return indexedStage{}, fmt.Errorf("stuntgres: missing row locking scope")
	}
	previous := state.locking
	state.locking = true
	child, err := openQueryNode(ctx, state, n.Child)
	state.locking = previous
	if err != nil {
		return indexedStage{}, err
	}
	if !queryHasPhysicalInput(n.Child) {
		return child, nil
	}
	if state.source.locks != nil {
		child.pending = appendPendingRowMarks(child.pending, n.Marks, state.source)
	}
	return child, nil
}

func isPGSequencesRelation(relation *catalog.Relation) bool {
	return relation.RelationKind == catalog.CatalogRelation && relation.Address().ObjectID == 12048
}

// SQL marks remain intact for binding, admission and clause diagnostics.
// The expanded pg_sequences view has no represented physical tuple to mark;
// its admitted-row sequence helper locks are a separate execution obligation.
func effectiveQueryRowMark(relation *catalog.Relation, mark plan.RowMark) plan.RowMark {
	if isPGSequencesRelation(relation) {
		return plan.RowMark{}
	}
	return mark
}

func mergeQueryRowMark(a, b plan.RowMark) plan.RowMark {
	if a.DiagnosticMode == 0 {
		a.DiagnosticMode = b.DiagnosticMode
	}
	if a.Mode == 0 {
		a.Order = b.Order
	}
	a.Mode = strongerQueryRowMode(a.Mode, b.Mode)
	if b.Wait > a.Wait {
		a.Wait = b.Wait
	}
	return a
}

// Resolve the physical expansion once at each existing demand frontier. The
// candidate loop only consults this immutable manifest.
func appendPendingRowMarks(dst []queryPendingMark, marks []plan.RowMark, inputs *querySource) []queryPendingMark {
	for i, mark := range marks {
		if mark.Mode == 0 {
			continue
		}
		source := querySourceAt(inputs, i)
		occurrences := appendQueryOccurrences(nil, source)
		// Expansion must apply the same effective decision as source capture:
		// a stored/derived/join boundary must not resurrect an SQL-only mark.
		physical := occurrences[:0]
		for _, occurrence := range occurrences {
			if effectiveQueryRowMark(inputs.locks.leaves[occurrence].leaf.relation, mark).Mode != 0 {
				physical = append(physical, occurrence)
			}
		}
		occurrences = physical
		if len(occurrences) != 0 {
			dst = append(dst, queryPendingMark{occurrences: occurrences, mark: mark})
		}
	}
	return dst
}
