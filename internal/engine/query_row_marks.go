package engine

import (
	"fmt"
	"stuntgres/internal/parser"
)

// Strength and wait precedence are independent. A manifest belongs to a bound
// query occurrence, never to its physical relation or column namespace.
type queryRowMark struct {
	mode           relationLockMode
	wait           parser.RowLockWait
	diagnosticMode relationLockMode
	// One-based first insertion order, independent of FROM order and upgrades.
	order int
}

func isPGSequencesRelation(relation *relationDescriptor) bool {
	return relation.relationKind == catalogRelation && relation.address.ObjectID == 12048
}

// SQL marks remain intact for binding, admission and clause diagnostics.
// The expanded pg_sequences view has no represented physical tuple to mark;
// its admitted-row sequence helper locks are a separate execution obligation.
func effectiveQueryRowMark(relation *relationDescriptor, mark queryRowMark) queryRowMark {
	if isPGSequencesRelation(relation) {
		return queryRowMark{}
	}
	return mark
}

// Admission is execution-owned and keyed by the actual query syntax, never a
// prepared description or session. Its semantic error waits for normal analysis.
type queryLockAdmission struct {
	selectQuery    *parser.Select
	syntheticOwner bool // DML source roots are regenerated on each visitor traversal.
	marks          []queryRowMark
	err            error
	indices        map[*parser.TableReference]int
	queries        map[*parser.Select]*queryLockAdmission
	ordered        []*queryLockAdmission
	tableQueries   map[*parser.TableReference]preparedSource // Query and exact pre-wait identity.
	futureCTE      map[*parser.TableReference]string
	virtualCTE     map[*parser.TableReference]bool
}

func mergeQueryRowMark(a, b queryRowMark) queryRowMark {
	if a.diagnosticMode == 0 {
		a.diagnosticMode = b.diagnosticMode
	}
	if a.mode == 0 {
		a.order = b.order
	}
	a.mode = strongerQueryRowMode(a.mode, b.mode)
	if b.wait > a.wait {
		a.wait = b.wait
	}
	return a
}

func queryRowDiagnostic(mark queryRowMark) relationLockMode {
	if mark.diagnosticMode != 0 {
		return mark.diagnosticMode
	}
	return mark.mode
}

func queryRootRowMarks(node LogicalNode) []queryRowMark {
	for {
		switch n := node.(type) {
		case *projectNode:
			node = n.child
		case *limitNode:
			node = n.child
		case *evaluationNode:
			node = n.child
		case *rowLockNode:
			return n.marks
		default:
			return nil
		}
	}
}

func queryOccurrenceMark(marks []queryRowMark, occurrence int, inherited queryRowMark) queryRowMark {
	if occurrence < len(marks) {
		return mergeQueryRowMark(inherited, marks[occurrence])
	}
	return inherited
}

// OF sees child relation occurrences through a whole-join alias, unlike the
// expression namespace. A relation's own alias still hides its original name.
func queryLockTargets(from parser.FromItem, clauses []parser.RowLockClause, virtual ...map[*parser.TableReference]bool) ([]queryRowMark, error) {
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
	marks := make([]queryRowMark, len(indices))
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
		case *parser.DerivedQuery:
			var selected []int
			childTargets, _ := queryLockTargets(n.Query.From, []parser.RowLockClause{{Mode: "FOR KEY SHARE"}}, virtual...)
			childIndex := 0
			_ = parser.VisitQuerySources(&parser.Select{From: n.Query.From}, nil, func(table *parser.TableReference, _ *parser.Select) error {
				if childIndex < len(childTargets) && childTargets[childIndex].mode != 0 {
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
		mark := queryRowMark{mode: mode, wait: clause.Wait, diagnosticMode: mode}
		if len(clause.Targets) == 0 {
			for _, target := range targets {
				for _, i := range target.occurrences {
					if marks[i].mode == 0 {
						order++
						mark.order = order
					}
					marks[i] = mergeQueryRowMark(marks[i], mark)
				}
			}
			continue
		}
		for _, name := range clause.Targets {
			if len(name.Parts) != 1 {
				return marks, expressionError("42601", clause.Mode+" must specify unqualified relation names", name.Parts[0].Pos)
			}
			found := false
			for _, target := range targets {
				if target.name != name.Parts[0].Text {
					continue
				}
				found = true
				if target.cte {
					return marks, expressionError("0A000", clause.Mode+" cannot be applied to a WITH query", name.Parts[0].Pos)
				}
				if target.join {
					return marks, expressionError("0A000", clause.Mode+" cannot be applied to a join", name.Parts[0].Pos)
				}
				if target.function {
					return marks, expressionError("0A000", clause.Mode+" cannot be applied to a function", name.Parts[0].Pos)
				}
				for _, occurrence := range target.occurrences {
					if marks[occurrence].mode == 0 {
						order++
						mark.order = order
					}
					marks[occurrence] = mergeQueryRowMark(marks[occurrence], mark)
				}
				break
			}
			if !found {
				return marks, expressionError("42P01", fmt.Sprintf(`relation "%s" in %s clause not found in FROM clause`, name.Parts[0].Text, clause.Mode), name.Parts[0].Pos)
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

// Resolve the physical expansion once at each existing demand frontier. The
// candidate loop only consults this immutable manifest.
func appendPendingRowMarks(dst []queryPendingMark, marks []queryRowMark, inputs *querySource) []queryPendingMark {
	for i, mark := range marks {
		if mark.mode == 0 {
			continue
		}
		source := querySourceAt(inputs, i)
		occurrences := appendQueryOccurrences(nil, source)
		// Expansion must apply the same effective decision as source capture:
		// a stored/derived/join boundary must not resurrect an SQL-only mark.
		physical := occurrences[:0]
		for _, occurrence := range occurrences {
			if effectiveQueryRowMark(inputs.locks.leaves[occurrence].leaf.relation, mark).mode != 0 {
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
