package exec

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
type LockContext struct {
	Leaves []QueryLockLeaf
	// DECLARE-only physical manifests retain transparent lineage without
	// requesting the effective-version/provenance caches needed by rowmarks.
	// Ordinary initialized contexts remain semantic (the zero value).
	IdentityOnly bool
}

func (c *LockContext) RowMarked() bool {
	return c != nil && !c.IdentityOnly
}

type QueryLockLeaf struct {
	Leaf    Leaf
	Source  *Source
	Mark    plan.RowMark
	Changed bool
	Absent  bool
}

type QueryLockLineage struct {
	Root             plan.Node
	Inputs, Stage    *Source
	Selection        []int
	orderValues      []sqltype.Value
	CacheWidth       int
	cacheOccurrences []int
	CurrentOrder     []sqltype.Value
	CurrentIndex     int
	state            *State
}

func StrongerQueryRowMode(a, b lock.Mode) lock.Mode {
	for _, mode := range [...]lock.Mode{lock.ForUpdate, lock.ForNoKeyUpdate, lock.ForShare, lock.ForKeyShare} {
		if a == mode || b == mode {
			return mode
		}
	}
	return 0
}

func QuerySourceAt(bundle *Source, occurrence int) *Source {
	if bundle.Sources != nil {
		return bundle.Sources[occurrence]
	}
	return bundle
}

func QueryLockRecheckWidth(node plan.Node, inputs *Source) int {
	switch n := node.(type) {
	case *plan.FunctionFromNode:
		return len(n.Schema())
	case *plan.SetProjectNode:
		return 0 // Legal nonphysical inherited paths need no substitution scratch.
	case *plan.LimitNode:
		return QueryLockRecheckWidth(n.Child, inputs)
	case *plan.AggregateNode, *plan.WindowNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.SetNode, *plan.RecursiveUnionNode, *plan.WorktableScanNode:
		return 0 // Its input marks/provenance have already been consumed.
	case *plan.EvaluationNode:
		return QueryLockRecheckWidth(n.Child, inputs)
	case *plan.DerivedNode:
		if n.CTE != nil {
			return 0
		}
		return QueryLockRecheckWidth(n.Query.Root, inputs)
	case *plan.SourceNode:
		return queryLockSourceWidth(QuerySourceAt(inputs, n.Occurrence))
	case *plan.SingletonNode, *plan.ValuesNode:
		return 0
	case *plan.RowLockNode:
		return QueryLockRecheckWidth(n.Child, inputs)
	case *plan.ProjectNode:
		if n.FrozenValues {
			return 0
		}
		return len(n.Schema()) + QueryLockRecheckWidth(n.Child, inputs)
	case *plan.JoinNode:
		return len(n.Schema()) + QueryLockRecheckWidth(n.Left, inputs) + QueryLockRecheckWidth(n.Right, inputs)
	case *plan.FilterNode:
		return QueryLockRecheckWidth(n.Child, inputs)
	case *plan.SortNode:
		return QueryLockRecheckWidth(n.Child, inputs)
	}
	panic("invalid marked view recheck tree")
}

func queryLockSourceWidth(source *Source) int {
	if source.Input != nil {
		return len(source.Layout) + queryLockSourceWidth(source.Input)
	}
	if source.Lineage != nil {
		return QueryLockRecheckWidth(source.Lineage.Root, source.Lineage.Inputs)
	}
	return 0
}

func RecheckLockedSource(source *Source, rows [][]sqltype.Value, evaluation *Context, scratch []sqltype.Value, offset *int) ([]sqltype.Value, bool, error) {
	if source.Lineage != nil && source.Lineage.CurrentIndex == AbsentQueryIndex {
		return nil, true, nil
	}
	if source.Input != nil {
		row, ok, err := RecheckLockedSource(source.Input, rows, evaluation, scratch, offset)
		if err != nil || !ok {
			return nil, ok, err
		}
		frame := scratch[*offset : *offset+len(source.Layout)]
		*offset += len(frame)
		for i, slot := range source.Layout {
			if slot >= 0 {
				frame[i] = row[slot]
			}
		}
		return frame, true, nil
	}
	if source.Lineage != nil {
		if !changedQueryLockSource(source.Lineage.Stage, evaluation.LiveHost().QueryLocks()) {
			return source.Row(source.Lineage.CurrentIndex), true, nil
		}
		child := *evaluation
		lineage := source.Lineage
		if lineage.state != nil {
			child.Frame = lineage.state.Frame
			if child.Frame == 0 {
				child.Frame = 1
			}
			child.Outer, child.Query = lineage.state.Outer, lineage.state
			child.Parameters, child.Input, child.Output = lineage.state.Parameters, lineage.state.Input, lineage.state.Output
		}
		child.OrderValues = lineage.CurrentOrder
		frozenIndex := lineage.CurrentIndex
		if lineage.Selection != nil {
			frozenIndex = lineage.Selection[frozenIndex]
		}
		frozen := lineage.Stage.Row(frozenIndex)
		child.BindQueryIdentity(lineage.Stage, frozenIndex)
		return RecheckLockedQuery(lineage.Root, lineage.Inputs, rows, &child, scratch, offset, frozen)
	}
	row := rows[source.Occurrence]
	if source.RLS != nil && row != nil {
		pass, _, err := EvaluateRowSecurity(source.RLS, evaluation, row)
		if err != nil || !pass {
			return nil, false, err
		}
	}
	return row, true, nil
}

func RecheckLockedQuery(node plan.Node, inputs *Source, rows [][]sqltype.Value, evaluation *Context, scratch []sqltype.Value, offset *int, frozen []sqltype.Value) ([]sqltype.Value, bool, error) {
	if absentLockedQuery(node, inputs, evaluation.LiveHost().QueryLocks()) {
		return nil, true, nil
	}
	switch n := node.(type) {
	case *plan.DerivedNode:
		if n.CTE != nil {
			return frozen, true, nil
		}
		if evaluation.Query == nil || evaluation.Query.derived[n] == nil {
			return nil, false, fmt.Errorf("stuntgres: missing derived rowmark lineage")
		}
		return RecheckLockedSource(evaluation.Query.derived[n], rows, evaluation, scratch, offset)
	case *plan.ValuesNode:
		return frozen, true, nil
	case *plan.FunctionFromNode:
		// PostgreSQL 18.6 keeps the function-scan tuple a row was produced
		// with; the recheck refreshes only locked rows and requalifies.
		return frozen, true, nil
	case *plan.DistinctNode, *plan.DistinctOnNode, *plan.SetNode, *plan.RecursiveUnionNode, *plan.WorktableScanNode:
		return frozen, true, nil // Its source marks were settled during materialization.
	case *plan.LimitNode:
		return RecheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
	case *plan.EvaluationNode:
		row, ok, err := RecheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
		if err != nil || !ok {
			return nil, ok, err
		}
		evaluation.Row = row
		for i, expr := range n.Expressions {
			value, err := expr.Eval(evaluation)
			if err != nil {
				return nil, false, err
			}
			evaluation.OrderValues[n.Base+i] = value
		}
		return row, true, nil
	case *plan.SourceNode:
		return RecheckLockedSource(QuerySourceAt(inputs, n.Occurrence), rows, evaluation, scratch, offset)
	case *plan.SingletonNode:
		return nil, true, nil
	case *plan.RowLockNode:
		return RecheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
	case *plan.SortNode:
		row, ok, err := RecheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
		if err != nil || !ok {
			return nil, ok, err
		}
		if err := RefreshLockedOrder(n, row, evaluation); err != nil {
			return nil, false, err
		}
		return row, true, nil
	case *plan.FilterNode:
		row, ok, err := RecheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
		if err != nil || !ok {
			return nil, ok, err
		}
		evaluation.Row = row
		matches, err := UpdateMatches(evaluation, n.Predicate)
		return row, matches, err
	case *plan.ProjectNode:
		if n.FrozenValues {
			return frozen, true, nil
		}
		row, ok, err := RecheckLockedQuery(n.Child, inputs, rows, evaluation, scratch, offset, frozen)
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
		left, ok, err := RecheckLockedQuery(n.Left, inputs, rows, evaluation, scratch, offset, frozen[:n.LeftWidth])
		if err != nil || !ok {
			return nil, ok, err
		}
		right, ok, err := RecheckLockedQuery(n.Right, inputs, rows, evaluation, scratch, offset, frozen[n.LeftWidth:n.LeftWidth+n.RightWidth])
		if err != nil || !ok {
			return nil, ok, err
		}
		frame := scratch[*offset : *offset+len(n.Schema())]
		*offset += len(frame)
		leftAbsent := absentLockedQuery(n.Left, inputs, evaluation.LiveHost().QueryLocks())
		rightAbsent := absentLockedQuery(n.Right, inputs, evaluation.LiveHost().QueryLocks())
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
			matches, err := UpdateMatches(evaluation, n.Predicate)
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
func ArbitrateViewQuery(ctx context.Context, state *State, node plan.Node, child Stage) (Stage, error) {
	locks := state.Source.Locks
	due := make([]plan.RowMark, len(locks.Leaves))
	for _, mark := range child.Pending {
		for _, occurrence := range mark.Occurrences {
			due[occurrence] = MergeQueryRowMark(due[occurrence], mark.Mark)
		}
	}
	for i, mark := range due {
		if mark.Mode != 0 {
			due[i] = MergeQueryRowMark(mark, locks.Leaves[i].Mark)
		}
	}
	occurrences := AppendQueryOccurrences(nil, child.Source)
	provenance := make([]int, 0, len(occurrences))
	versions := make([]storage.RowVersion, 0, len(occurrences))
	currentRows := make([][]sqltype.Value, len(locks.Leaves))
	scratch := make([]sqltype.Value, QueryLockRecheckWidth(node, state.Source))
	evaluation := state.Evaluation(nil)
	evaluation.Context = nil
	joined := child.Source.Materialized
	source := child.Source
	if !joined {
		source = &Source{Anonymous: [][]sqltype.Value{}, Materialized: true, Occurrences: occurrences, Authority: state.Source.Authority, Host: state.Source.Host}
		source.PresenceSlots, source.PresenceCount = child.Source.PresenceSlots, child.Source.PresenceCount
		source.PresenceConstant = child.Source.PresenceConstant
	}
	source.Occurrences = occurrences
	out := ProducingStage(&child, child.Columns)
	out.Source, out.Expressions, out.Evaluation = source, child.Expressions, child.Evaluation
	out.Pending = nil
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		defer func() { evaluation.Context = nil }()
		host := state.Source.LiveHost()
		if host == nil {
			return false, fmt.Errorf("stuntgres: missing row locking scope")
		}
		for {
			i, present, err := child.NextOccurrence(ctx)
			if err != nil || !present {
				return present, err
			}
			evaluation.setContext(ctx)
			index := child.Index(i)
			SetQueryLineage(child.Source, index)
			provenance = AppendQueryProvenance(provenance[:0], child.Source, index)
			evaluation.QueryOccurrences, evaluation.QueryProvenance = occurrences, provenance
			versions = AppendQueryVersions(versions[:0], child.Source, index, locks)
			// Load the frozen frame before mapping latest physical values. A
			// nonidentity inheritance segment reuses its capture-owned scratch;
			// loading it afterward would overwrite currentRows with old values.
			row := child.Source.Row(index)
			live, changed := true, false
			for j, occurrence := range occurrences {
				entry := locks.Leaves[occurrence]
				visible := provenance[j]
				locks.Leaves[occurrence].Absent = visible == AbsentQueryIndex
				if visible == AbsentQueryIndex {
					currentRows[occurrence] = nil
					locks.Leaves[occurrence].Changed = false
					continue
				}
				mark := due[occurrence]
				locator := entry.Source.PhysicalRow(visible, entry.Leaf, versions[j])
				if mark.Mode == 0 {
					currentRows[occurrence] = locator.Values(versions[j])
					locks.Leaves[occurrence].Changed = false
					continue
				}
				candidate := locator.Version
				acquired, err := host.AcquireQueryRow(locator.Leaf, candidate.Row, mark)
				if err != nil {
					return false, err
				}
				if !acquired {
					live = false
					break
				}
				current, present, err := host.LatestQueryRow(locator.Leaf, candidate, locator.Hint, mark.Mode)
				if err != nil {
					return false, err
				}
				live = live && present
				changed = changed || current.Version != candidate.Version
				currentRows[occurrence] = locator.Values(current)
				versions[j] = current
				locks.Leaves[occurrence].Changed = current.Version != candidate.Version
			}
			if !live {
				continue
			}
			evaluation.setQueryPresence(child.Source, index)
			if changed {
				evaluation.OrderValues = child.OrderRow(i)
				offset := 0
				previousRows := evaluation.QueryCurrentRows
				evaluation.QueryCurrentRows = currentRows
				replacement, ok, err := RecheckLockedQuery(node, state.Source, currentRows, &evaluation, scratch, &offset, row)
				evaluation.QueryCurrentRows = previousRows
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
					copy(child.Source.Row(index), row)
				}
				width := len(occurrences)
				if width > 0 {
					if missing := (index+1)*width - len(source.Versions); missing > 0 {
						if source.VersionValid == nil {
							source.VersionValid = make([]bool, len(source.Versions)/width)
							for i := range source.VersionValid {
								source.VersionValid[i] = true
							}
						}
						source.VersionValid = append(source.VersionValid, make([]bool, index+1-len(source.VersionValid))...)
						source.Versions = append(source.Versions, make([]storage.RowVersion, missing)...)
					}
					copy(source.Versions[index*width:(index+1)*width], versions)
					if source.VersionValid != nil {
						source.VersionValid[index] = true
					}
				}
				out.Selection = append(out.Selection, index)
			} else {
				mapped := false
				if child.Source.Inheritance != nil {
					segment, _ := child.Source.inheritanceSegment(index)
					mapped = segment.Layout != nil
				}
				if changed || mapped || child.Source.Input != nil || child.Source.Final != nil && child.Source.Final.Source != nil {
					row = append([]sqltype.Value(nil), row...)
				}
				out.Selection = append(out.Selection, len(source.Anonymous))
				source.Anonymous = append(source.Anonymous, row)
				if source.PresenceCount != 0 && len(source.PresenceConstant) == 0 {
					source.presenceFlat = append(source.presenceFlat, child.Source.PresenceRow(index)...)
				}
				source.Provenance = append(source.Provenance, provenance...)
				source.Versions = append(source.Versions, versions...)
				source.FlatCount++
			}
			out.CacheWidth, out.OrderValues = child.CacheWidth, child.OrderValues
			if child.CacheWidth != 0 {
				out.CacheOccurrences = append(out.CacheOccurrences, child.orderOccurrence(i))
			}
			return true, ctx.Err()
		}
	}
	return out, nil
}

func QueryHasPhysicalInput(node plan.Node) bool {
	if node == nil {
		return false
	}
	if _, ok := node.(*plan.SourceNode); ok {
		return true
	}
	left, right := plan.QueryInputs(node)
	return QueryHasPhysicalInput(left) || QueryHasPhysicalInput(right)
}

// AbsentQueryIndex is an absent physical occurrence, never a real NULL-valued row.
const AbsentQueryIndex = -1

// Input adapters translate row identity exactly as they translate row cells.
func (source *Source) InputIndex(index int) int {
	if index != AbsentQueryIndex && source.WindowIndices != nil {
		return source.WindowIndices[index]
	}
	return index
}

func queryOccurrenceWidth(source *Source) int {
	if source.Input != nil {
		return queryOccurrenceWidth(source.Input)
	}
	if source.Lineage != nil {
		return queryOccurrenceWidth(source.Lineage.Stage)
	}
	if source.Singleton {
		return 0
	}
	if source.Materialized {
		return len(source.Occurrences)
	}
	return 1
}

func AppendQueryOccurrences(dst []int, source *Source) []int {
	if source.Input != nil {
		return AppendQueryOccurrences(dst, source.Input)
	}
	if source.Lineage != nil {
		return AppendQueryOccurrences(dst, source.Lineage.Stage)
	}
	if source.Singleton {
		return dst
	}
	if source.Materialized {
		return append(dst, source.Occurrences...)
	}
	return append(dst, source.Occurrence)
}

func AppendQueryProvenance(dst []int, source *Source, index int) []int {
	if index == AbsentQueryIndex {
		SetQueryLineage(source, index)
		for range queryOccurrenceWidth(source) {
			dst = append(dst, AbsentQueryIndex)
		}
		return dst
	}
	if source.Input != nil {
		return AppendQueryProvenance(dst, source.Input, source.InputIndex(index))
	}
	if source.Lineage != nil {
		lineage := source.Lineage
		lineage.CurrentIndex = index
		if lineage.CacheWidth != 0 {
			occurrence := index
			if lineage.cacheOccurrences != nil {
				occurrence = lineage.cacheOccurrences[index]
			}
			lineage.CurrentOrder = lineage.orderValues[occurrence*lineage.CacheWidth : (occurrence+1)*lineage.CacheWidth]
		}
		if source.Lineage.Selection != nil {
			index = source.Lineage.Selection[index]
		}
		return AppendQueryProvenance(dst, source.Lineage.Stage, index)
	}
	if source.Singleton {
		return dst
	}
	if source.Materialized {
		width := len(source.Occurrences)
		return append(dst, source.Provenance[index*width:(index+1)*width]...)
	}
	return append(dst, index)
}

func openRowLockNode(ctx context.Context, state *State, n *plan.RowLockNode) (Stage, error) {
	if state.Source.LiveHost() == nil {
		return Stage{}, fmt.Errorf("stuntgres: missing row locking scope")
	}
	previous := state.Locking
	state.Locking = true
	child, err := OpenNode(ctx, state, n.Child)
	state.Locking = previous
	if err != nil {
		return Stage{}, err
	}
	if !QueryHasPhysicalInput(n.Child) {
		return child, nil
	}
	if state.Source.Locks != nil {
		child.Pending = AppendPendingRowMarks(child.Pending, n.Marks, state.Source)
	}
	return child, nil
}

func IsPGSequencesRelation(relation *catalog.Relation) bool {
	return relation.RelationKind == catalog.CatalogRelation && relation.Address().ObjectID == 12048
}

// SQL marks remain intact for binding, admission and clause diagnostics.
// The expanded pg_sequences view has no represented physical tuple to mark;
// its admitted-row sequence helper locks are a separate execution obligation.
func EffectiveQueryRowMark(relation *catalog.Relation, mark plan.RowMark) plan.RowMark {
	if IsPGSequencesRelation(relation) {
		return plan.RowMark{}
	}
	return mark
}

func MergeQueryRowMark(a, b plan.RowMark) plan.RowMark {
	if a.DiagnosticMode == 0 {
		a.DiagnosticMode = b.DiagnosticMode
	}
	if a.Mode == 0 {
		a.Order = b.Order
	}
	a.Mode = StrongerQueryRowMode(a.Mode, b.Mode)
	if b.Wait > a.Wait {
		a.Wait = b.Wait
	}
	return a
}

// Resolve the physical expansion once at each existing demand frontier. The
// candidate loop only consults this immutable manifest.
func AppendPendingRowMarks(dst []queryPendingMark, marks []plan.RowMark, inputs *Source) []queryPendingMark {
	for i, mark := range marks {
		if mark.Mode == 0 {
			continue
		}
		source := QuerySourceAt(inputs, i)
		occurrences := AppendQueryOccurrences(nil, source)
		// Expansion must apply the same effective decision as source capture:
		// a stored/derived/join boundary must not resurrect an SQL-only mark.
		physical := occurrences[:0]
		for _, occurrence := range occurrences {
			if EffectiveQueryRowMark(inputs.Locks.Leaves[occurrence].Leaf.Relation, mark).Mode != 0 {
				physical = append(physical, occurrence)
			}
		}
		occurrences = physical
		if len(occurrences) != 0 {
			dst = append(dst, queryPendingMark{Occurrences: occurrences, Mark: mark})
		}
	}
	return dst
}
