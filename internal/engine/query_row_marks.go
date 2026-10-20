package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

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
