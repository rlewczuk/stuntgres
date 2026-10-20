package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

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

// Admission is execution-owned and keyed by the actual query syntax, never a
// prepared description or session. Its semantic error waits for normal analysis.
type queryLockAdmission struct {
	selectQuery     *parser.Select
	syntheticOwner  bool // DML source roots are regenerated on each visitor traversal.
	marks           []plan.RowMark
	err             error
	indices         map[*parser.TableReference]int
	queries         map[*parser.Select]*queryLockAdmission
	ordered         []*queryLockAdmission
	tableQueries    map[*parser.TableReference]preparedSource // Query and exact pre-wait identity.
	futureCTE       map[*parser.TableReference]string
	virtualCTE      map[*parser.TableReference]bool
	mutationTargets []admittedMutationTarget
}

func queryRowDiagnostic(mark plan.RowMark) lock.Mode {
	if mark.DiagnosticMode != 0 {
		return mark.DiagnosticMode
	}
	return mark.Mode
}

func queryRootRowMarks(node plan.Node) []plan.RowMark {
	for {
		switch n := node.(type) {
		case *plan.ProjectNode:
			node = n.Child
		case *plan.LimitNode:
			node = n.Child
		case *plan.EvaluationNode:
			node = n.Child
		case *plan.RowLockNode:
			return n.Marks
		default:
			return nil
		}
	}
}

func queryOccurrenceMark(marks []plan.RowMark, occurrence int, inherited plan.RowMark) plan.RowMark {
	if occurrence < len(marks) {
		return mergeQueryRowMark(inherited, marks[occurrence])
	}
	return inherited
}

// OF sees child relation occurrences through a whole-join alias, unlike the
// expression namespace. A relation's own alias still hides its original name.
func queryLockTargets(from parser.FromItem, clauses []parser.RowLockClause, virtual ...map[*parser.TableReference]bool) ([]plan.RowMark, error) {
	if len(clauses) == 0 {
		return nil, nil
	}
	type target struct {
		name        string
		occurrences []int
		join        bool
		cte         bool
		function    bool
	}
	indices := make(map[*parser.TableReference]int)
	_ = parser.VisitQuerySources(&parser.Select{From: from}, nil, func(table *parser.TableReference, _ *parser.Select) error { indices[table] = len(indices); return nil })
	marks := make([]plan.RowMark, len(indices))
	var targets []target
	var visit func(parser.FromItem, string)
	visit = func(item parser.FromItem, alias string) {
		switch n := item.(type) {
		case *parser.TableReference:
			name := alias
			if name == "" {
				name = n.Name.Parts[len(n.Name.Parts)-1].Text
			}
			targets = append(targets, target{name: name, occurrences: []int{indices[n]}})
			if len(virtual) != 0 && virtual[0][n] {
				targets[len(targets)-1].occurrences = nil
				targets[len(targets)-1].cte = true
			}
		case *parser.FunctionTable:
			name := alias
			if name == "" {
				parts := n.Calls[0].Name.Parts
				name = parts[len(parts)-1].Text
			}
			targets = append(targets, target{name: name, function: true})
		case *parser.ValuesQuery:
			if alias != "" {
				targets = append(targets, target{name: alias})
			}
		case *parser.FunctionFrom:
			if alias == "" {
				alias = n.Calls[0].Name.Parts[len(n.Calls[0].Name.Parts)-1].Text
			}
			targets = append(targets, target{name: alias, function: true})
		case *parser.DerivedQuery:
			var selected []int
			childTargets, _ := queryLockTargets(n.Query.From, []parser.RowLockClause{{Mode: "FOR KEY SHARE"}}, virtual...)
			childIndex := 0
			_ = parser.VisitQuerySources(&parser.Select{From: n.Query.From}, nil, func(table *parser.TableReference, _ *parser.Select) error {
				if childIndex < len(childTargets) && childTargets[childIndex].Mode != 0 {
					selected = append(selected, indices[table])
				}
				childIndex++
				return nil
			})
			targets = append(targets, target{name: alias, occurrences: selected})
		case *parser.AliasReference:
			if alias == "" {
				alias = n.Name.Text
			}
			visit(n.Input, alias)
		case *parser.JoinReference:
			if alias != "" {
				targets = append(targets, target{name: alias, join: true})
			}
			if n.UsingAlias != nil {
				targets = append(targets, target{name: n.UsingAlias.Text, join: true})
			}
			visit(n.Left, "")
			visit(n.Right, "")
		}
	}
	visit(from, "")
	order := 0
	for _, clause := range clauses {
		mode := queryRowMode(clause.Mode)
		mark := plan.RowMark{Mode: mode, Wait: clause.Wait, DiagnosticMode: mode}
		if len(clause.Targets) == 0 {
			for _, target := range targets {
				for _, i := range target.occurrences {
					if marks[i].Mode == 0 {
						order++
						mark.Order = order
					}
					marks[i] = mergeQueryRowMark(marks[i], mark)
				}
			}
			continue
		}
		for _, name := range clause.Targets {
			if len(name.Parts) != 1 {
				return marks, expr.ErrorAt("42601", clause.Mode+" must specify unqualified relation names", name.Parts[0].Pos)
			}
			found := false
			for _, target := range targets {
				if target.name != name.Parts[0].Text {
					continue
				}
				found = true
				if target.function {
					return marks, expr.ErrorAt("0A000", clause.Mode+" cannot be applied to a function", name.Parts[0].Pos)
				}
				if target.cte {
					return marks, expr.ErrorAt("0A000", clause.Mode+" cannot be applied to a WITH query", name.Parts[0].Pos)
				}
				if target.join {
					return marks, expr.ErrorAt("0A000", clause.Mode+" cannot be applied to a join", name.Parts[0].Pos)
				}
				if target.function {
					return marks, expr.ErrorAt("0A000", clause.Mode+" cannot be applied to a function", name.Parts[0].Pos)
				}
				for _, occurrence := range target.occurrences {
					if marks[occurrence].Mode == 0 {
						order++
						mark.Order = order
					}
					marks[occurrence] = mergeQueryRowMark(marks[occurrence], mark)
				}
				break
			}
			if !found {
				return marks, expr.ErrorAt("42P01", fmt.Sprintf(`relation "%s" in %s clause not found in FROM clause`, name.Parts[0].Text, clause.Mode), name.Parts[0].Pos)
			}
		}
	}
	return marks, nil
}

func firstSetRowClause(sel *parser.Select) string {
	if len(sel.Locking) != 0 {
		return sel.Locking[0].Mode
	}
	if sel.Set != nil {
		if mode := firstSetRowClause(sel.Set.Left); mode != "" {
			return mode
		}
		return firstSetRowClause(sel.Set.Right)
	}
	return ""
}

func queryRowMode(clause string) lock.Mode {
	switch clause {
	case "FOR KEY SHARE":
		return lock.ForKeyShare
	case "FOR SHARE":
		return lock.ForShare
	case "FOR NO KEY UPDATE":
		return lock.ForNoKeyUpdate
	case "FOR UPDATE":
		return lock.ForUpdate
	}
	panic("invalid SELECT row lock mode")
}
