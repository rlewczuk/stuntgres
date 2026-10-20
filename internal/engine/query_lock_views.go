package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
func queryRowClause(mode lock.Mode) string {
	switch mode {
	case lock.ForKeyShare:
		return "FOR KEY SHARE"
	case lock.ForShare:
		return "FOR SHARE"
	case lock.ForNoKeyUpdate:
		return "FOR NO KEY UPDATE"
	case lock.ForUpdate:
		return "FOR UPDATE"
	}
	return ""
}
func queryRootRowMode(node plan.Node) lock.Mode {
	for {
		switch n := node.(type) {
		case *plan.ProjectNode:
			node = n.Child
		case *plan.LimitNode:
			node = n.Child
		case *plan.EvaluationNode:
			node = n.Child
		case *plan.RowLockNode:
			return n.FirstMode
		case *plan.DistinctNode, *plan.DistinctOnNode, *plan.AggregateNode:
			return 0 // Descendant marks are consumed at materialization.
		default:
			return 0
		}
	}
}
func (e *Engine) firstQueryRowModeLocked(scope *StatementScope, root plan.Node, leaves []queryLeaf) (lock.Mode, error) {
	view := e.privateCatalogLocked(scope.owner.catalog)
	if _, err := validateViewReadGraph(view, leaves); err != nil {
		return 0, err
	}
	if mode := queryRootRowMode(root); mode != 0 {
		return mode, nil
	}
	var visit func(*catalog.Relation, plan.RowMark, lock.Mode) (lock.Mode, error)
	visit = func(d *catalog.Relation, inherited plan.RowMark, diagnostic lock.Mode) (lock.Mode, error) {
		if d.RelationKind != catalog.ViewRelation {
			if inherited.Mode == 0 {
				return 0, nil
			}
			if diagnostic != 0 {
				return diagnostic, nil
			}
			return inherited.Mode, nil
		}
		if inherited.Mode != 0 {
			return queryRowDiagnostic(inherited), nil
		}
		if diagnostic == 0 {
			diagnostic = queryRootRowMode(viewOf(d).query.Root)
		}
		for i, leaf := range viewOf(d).sources {
			child, ok := view.Relation(leaf.ref.Address)
			if !ok || !storedViewReferenceValid(view, d, leaf.ref) {
				return 0, ids.ErrCatalogReplaced
			}
			first, err := visit(child, viewLeafRowMark(viewOf(d), i, inherited), diagnostic)
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
func viewHasStoredRowMarks(d *catalog.Relation, view catalog.Read) bool {
	seen := make(map[ids.ObjectAddress]bool)
	var visit func(*catalog.Relation) bool
	visit = func(d *catalog.Relation) bool {
		if d.RelationKind != catalog.ViewRelation || seen[d.Address()] {
			return false
		}
		seen[d.Address()] = true
		if queryRootRowMode(viewOf(d).query.Root) != 0 {
			return true
		}
		for _, leaf := range viewOf(d).sources {
			if leaf.mark.Mode != 0 {
				return true
			}
			if child, ok := view.Relation(leaf.ref.Address); ok && visit(child) {
				return true
			}
		}
		return false
	}
	return visit(d)
}
func viewRowLockLegality(d *viewDefinition, mode lock.Mode) error {
	if queryHasSetOutput(d.query.Root) {
		return nil // A stored set view is an opaque physical-lineage boundary.
	}
	return queryRowLockLegality(d.query.Root, mode)
}

// Only the stored query's output boundary stops inherited reference-kind
// context. Ordinary joins/derived queries retain their existing legality and
// sibling checks; a nested stored set view applies this boundary on its entry.
func queryHasSetOutput(node plan.Node) bool {
	for {
		switch n := node.(type) {
		case *plan.ProjectNode:
			node = n.Child
		case *plan.SortNode:
			node = n.Child
		case *plan.LimitNode:
			node = n.Child
		case *plan.EvaluationNode:
			node = n.Child
		case *plan.FilterNode:
			node = n.Child
		case *plan.SetNode, *plan.RecursiveUnionNode:
			return true
		default:
			return false
		}
	}
}

func queryRowLockLegality(root plan.Node, mode lock.Mode) error {
	if mode == 0 {
		return nil
	}
	project, ok := root.(*plan.ProjectNode)
	if !ok || project.Syntax == nil {
		return fmt.Errorf("stuntgres: missing rowmark query syntax")
	}
	suffix := ""
	switch {
	case project.Syntax.Set != nil:
		suffix = "UNION/INTERSECT/EXCEPT"
	case project.Syntax.Distinct != nil:
		suffix = "DISTINCT clause"
	case project.Syntax.GroupBy != nil:
		suffix = "GROUP BY clause"
	case project.Syntax.Having != nil:
		suffix = "HAVING clause"
	default:
		for node := project.Child; node != nil; {
			switch n := node.(type) {
			case *plan.ProjectNode:
				node = n.Child
			case *plan.SetProjectNode:
				node = n.Child
			case *plan.LimitNode:
				node = n.Child
			case *plan.EvaluationNode:
				node = n.Child
			case *plan.SortNode:
				node = n.Child
			case *plan.FilterNode:
				node = n.Child
			case *plan.RowLockNode:
				node = n.Child
			case *plan.AggregateNode:
				suffix = "aggregate functions"
				node = nil
			case *plan.WindowNode:
				suffix = "window functions"
				node = nil
			case *plan.DistinctNode, *plan.DistinctOnNode:
				suffix = "DISTINCT clause"
				node = nil
			default:
				node = nil
			}
		}
	}
	if suffix == "" && project.HasTargetSet && stringInheritedRowMark(viewFromRoot(project)) {
		suffix = "set-returning functions in the target list"
	}
	if suffix != "" {
		return sqlError("0A000", queryRowClause(mode)+" is not allowed with "+suffix)
	}
	var from func(plan.Node) error
	from = func(node plan.Node) error {
		switch n := node.(type) {
		case *plan.DerivedNode:
			if n.CTE != nil && n.CTE.Mutation != nil {
				return nil
			}
			if n.CTE != nil {
				return queryRowLockLegality(n.Query.Root, queryRootRowMode(n.Query.Root))
			}
			return queryRowLockLegality(n.Query.Root, strongerQueryRowMode(mode, queryRootRowMode(n.Query.Root)))
		case *plan.JoinNode:
			if err := from(n.Left); err != nil {
				return err
			}
			return from(n.Right)
		}
		return nil
	}
	return from(viewFromRoot(project))
}

// View lock pushdown marks relation/subquery RTEs, not computed function rows.
func stringInheritedRowMark(node plan.Node) bool {
	switch n := node.(type) {
	case *plan.SourceNode:
		return true
	case *plan.DerivedNode:
		return n.CTE == nil
	case *plan.JoinNode:
		return stringInheritedRowMark(n.Left) || stringInheritedRowMark(n.Right)
	}
	return false
}

// Called before any source expression is opened. Inherited rowmark legality,
// kind and authorization checks remain Execute-only.
func (e *Engine) initializeQueryLocksLocked(scope *StatementScope, root plan.Node, leaves []queryLeaf) error {
	marked := queryRootRowMode(root) != 0
	view := e.privateCatalogLocked(scope.owner.catalog)
	visibilityDemand, err := validateViewReadGraph(view, leaves)
	if err != nil {
		return err
	}
	scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || visibilityDemand
	var visit func(*catalog.Relation, plan.RowMark, bool) error
	visit = func(d *catalog.Relation, inherited plan.RowMark, referenceMarked bool) error {
		if d.RelationKind == catalog.ViewRelation {
			if queryHasSetOutput(viewOf(d).query.Root) {
				referenceMarked = false
			}
			referenceMarked = referenceMarked || queryRootRowMode(viewOf(d).query.Root) != 0
			marked = marked || queryRootRowMode(viewOf(d).query.Root) != 0
			if err := viewRowLockLegality(viewOf(d), queryRowDiagnostic(inherited)); err != nil {
				return err
			}
			for i, leaf := range viewOf(d).sources {
				child, ok := view.Relation(leaf.ref.Address)
				if !ok || !storedViewReferenceValid(view, d, leaf.ref) {
					return ids.ErrCatalogReplaced
				}
				if err := visit(child, viewLeafRowMark(viewOf(d), i, inherited), referenceMarked); err != nil {
					return err
				}
			}
			return nil
		}
		if isPGSequencesRelation(d) {
			return nil // Expanded catalog view: no represented physical tuple input.
		}
		if (referenceMarked || inherited.Mode != 0) && d.RelationKind == catalog.MaterializedViewRelation {
			return sqlError("42809", fmt.Sprintf("cannot lock rows in materialized view %q", d.Name()))
		}
		// PostgreSQL validates reference rowmark kinds even for an unselected
		// sequence sibling. This does not confer UPDATE ACLs or tuple grants.
		if referenceMarked && d.RelationKind == catalog.SequenceRelation {
			return sqlError("42809", fmt.Sprintf("cannot lock rows in sequence %q", d.Name()))
		}
		if inherited.Mode == 0 {
			return nil
		}
		marked = true
		if d.RelationKind == catalog.CatalogRelation {
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

func (v AuthorizationView) checkViewRowMarks(d *catalog.Relation, inherited plan.RowMark, invoker authorization.PrincipalRef) error {
	if _, err := validateViewReadGraph(v.Catalog, []queryLeaf{{relation: d}}); err != nil {
		return err
	}
	return v.checkViewRowMarksExpanded(d, inherited, invoker)
}

func (v AuthorizationView) checkViewRowMarksExpanded(d *catalog.Relation, inherited plan.RowMark, invoker authorization.PrincipalRef) error {
	if d.RelationKind != catalog.ViewRelation {
		return nil
	}
	if err := viewRowLockLegality(viewOf(d), queryRowDiagnostic(inherited)); err != nil {
		return err
	}
	principal := d.Owner
	if viewOf(d).invoker {
		principal = invoker
	}
	v.State.CurrentUser = principal
	for i, leaf := range viewOf(d).sources {
		mark := viewLeafRowMark(viewOf(d), i, inherited)
		child, ok := v.Catalog.Relation(leaf.ref.Address)
		if !ok || !storedViewReferenceValid(v.Catalog, d, leaf.ref) {
			return ids.ErrCatalogReplaced
		}
		indices := make([]int, 0, len(leaf.reads))
		for _, attribute := range leaf.reads {
			found := false
			for slot, live := range child.Attributes {
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
		use, err := storedViewUseReference(v.Catalog, d, leaf.ref)
		if err != nil {
			return err
		}
		if err := v.checkColumns(binding{ref: use, relation: child}, authorization.SelectPrivilege, indices); err != nil {
			return err
		}
		if effectiveQueryRowMark(child, mark).Mode != 0 {
			if err := v.checkColumns(binding{ref: use, relation: child}, authorization.UpdatePrivilege, nil); err != nil {
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

// Inherited marks follow physical lineage, unlike general source/dependency
// traversal: a materialized set still reads both arms but exposes neither's rows.
func queryRowMarkReachesSource(node plan.Node, occurrence int) bool {
	if node == nil {
		return false
	}
	if _, ok := node.(*plan.SetNode); ok {
		return false
	}
	if source, ok := node.(*plan.SourceNode); ok {
		return source.Occurrence == occurrence
	}
	left, right := plan.QueryInputs(node)
	return queryRowMarkReachesSource(left, occurrence) || queryRowMarkReachesSource(right, occurrence)
}

func viewLeafRowMark(view *viewDefinition, occurrence int, inherited plan.RowMark) plan.RowMark {
	mark := view.sources[occurrence].mark
	if inherited.Mode != 0 && queryRowMarkReachesSource(view.query.Root, occurrence) {
		mark = mergeQueryRowMark(inherited, mark)
	}
	return mark
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
