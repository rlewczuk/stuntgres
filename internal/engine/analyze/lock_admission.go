package analyze

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// RowLockClause spells a row lock mode as its SQL clause, or "" for none.
func RowLockClause(mode lock.Mode) string {
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

// LockAdmission is the row-lock admission of one statement, computed from its
// syntax before binding: the marks of every SELECT level, the table
// occurrences each level locks, and the data-modifying CTE targets.
// Admission is execution-owned and keyed by the actual query syntax, never a
// prepared description or session. Its semantic error waits for normal analysis.
type LockAdmission struct {
	SelectQuery     *parser.Select
	syntheticOwner  bool // DML source roots are regenerated on each visitor traversal.
	Marks           []plan.RowMark
	Err             error
	Indices         map[*parser.TableReference]int
	Queries         map[*parser.Select]*LockAdmission
	Ordered         []*LockAdmission
	TableQueries    map[*parser.TableReference]PreparedSource // Query and exact pre-wait identity.
	FutureCTE       map[*parser.TableReference]string
	VirtualCTE      map[*parser.TableReference]bool
	MutationTargets []MutationTarget
}

// RowLockDiagnostic is the mode a diagnostic names for mark.
func RowLockDiagnostic(mark plan.RowMark) lock.Mode {
	if mark.DiagnosticMode != 0 {
		return mark.DiagnosticMode
	}
	return mark.Mode
}

// RootRowMarks returns the row marks of a query root's RowLockNode, if any.
func RootRowMarks(node plan.Node) []plan.RowMark {
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

// RowLockTargets resolves the locking clauses of one query level to the
// row marks of its FROM occurrences. OF sees child relation occurrences through a whole-join alias, unlike the
// expression namespace. A relation's own alias still hides its original name.
func RowLockTargets(from parser.FromItem, clauses []parser.RowLockClause, virtual ...map[*parser.TableReference]bool) ([]plan.RowMark, error) {
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
			childTargets, _ := RowLockTargets(n.Query.From, []parser.RowLockClause{{Mode: "FOR KEY SHARE"}}, virtual...)
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
		mode := RowLockMode(clause.Mode)
		mark := plan.RowMark{Mode: mode, Wait: clause.Wait, DiagnosticMode: mode}
		if len(clause.Targets) == 0 {
			for _, target := range targets {
				for _, i := range target.occurrences {
					if marks[i].Mode == 0 {
						order++
						mark.Order = order
					}
					marks[i] = exec.MergeQueryRowMark(marks[i], mark)
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
					marks[occurrence] = exec.MergeQueryRowMark(marks[occurrence], mark)
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

// FirstSetRowClause returns the first locking clause in a set operation tree.
func FirstSetRowClause(sel *parser.Select) string {
	if len(sel.Locking) != 0 {
		return sel.Locking[0].Mode
	}
	if sel.Set != nil {
		if mode := FirstSetRowClause(sel.Set.Left); mode != "" {
			return mode
		}
		return FirstSetRowClause(sel.Set.Right)
	}
	return ""
}

// RowLockMode parses a SELECT locking clause; it panics on any other text.
func RowLockMode(clause string) lock.Mode {
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

// NewLockAdmission computes the admission of statement and returns it with
// its locked table occurrences and SELECT levels in admission order.
func NewLockAdmission(statement parser.Statement) (LockAdmission, []*parser.TableReference, []*parser.Select, error) {
	admissions := make(map[*parser.Select]*LockAdmission)
	var ordered []*LockAdmission
	tableQueries := make(map[*parser.TableReference]PreparedSource)
	future := make(map[*parser.TableReference]string)
	virtual := make(map[*parser.TableReference]bool)
	admit := func(query *parser.Select) *LockAdmission {
		if prior := admissions[query]; prior != nil {
			return prior
		}
		indices := make(map[*parser.TableReference]int)
		_ = parser.VisitQuerySources(&parser.Select{From: query.From}, nil, func(table *parser.TableReference, _ *parser.Select) error { indices[table] = len(indices); return nil })
		admission := &LockAdmission{SelectQuery: query, Indices: indices}
		admissions[query] = admission
		ordered = append(ordered, admission)
		return admission
	}
	var tables []*parser.TableReference
	var queries []*parser.Select
	if err := parser.VisitQuerySources(statement, func(query *parser.Select) error { admit(query); return nil }, func(table *parser.TableReference, query *parser.Select) error {
		if admissions[query] == nil {
			admit(query).syntheticOwner = true
		}
		tableQueries[table] = PreparedSource{Query: query}
		tables = append(tables, table)
		queries = append(queries, query)
		return nil
	}, func(table *parser.TableReference, isVirtual bool, pending string) {
		virtual[table] = isVirtual
		future[table] = pending
	}); err != nil {
		return LockAdmission{}, nil, nil, err
	}
	for _, a := range ordered {
		a.Marks, a.Err = RowLockTargets(a.SelectQuery.From, a.SelectQuery.Locking, virtual)
	}
	return LockAdmission{Queries: admissions, Ordered: ordered, TableQueries: tableQueries, FutureCTE: future, VirtualCTE: virtual}, tables, queries, nil
}

// The original table pointer is stable for every occurrence. Only real SELECT
// syntax has a stable owning query; the parser regenerates DML source roots.
func (a *LockAdmission) RetainedQuery(table *parser.TableReference) *parser.Select {
	query := a.TableQueries[table].Query
	if owner := a.Queries[query]; owner != nil && owner.syntheticOwner {
		return nil
	}
	return query
}

func (a *LockAdmission) TableMark(table *parser.TableReference) plan.RowMark {
	mark := plan.RowMark{}
	if a != nil {
		for _, admission := range a.Ordered {
			if index, ok := admission.Indices[table]; ok && index < len(admission.Marks) {
				mark = exec.MergeQueryRowMark(mark, admission.Marks[index])
			}
		}
	}
	return mark
}

// PreparedSource is a locked table occurrence: its owning query and the
// identity admitted before any lock wait.
type PreparedSource struct {
	Query   *parser.Select
	Ref     ids.ObjectRef
	Lexical ids.ObjectRef // Statement-local name observation, never a retained lock choice.
}

// MutationTarget is the target identity admitted for a data-modifying CTE.
type MutationTarget struct {
	Statement parser.Statement
	Ref       ids.ObjectRef
}

// FutureCTEError adds PostgreSQL's forward-reference detail and hint to a
// missing-relation error for a name that is a later WITH item.
func FutureCTEError(err error, name string) error {
	var diagnostic *sqlstate.Error
	if name == "" || !errors.As(err, &diagnostic) || diagnostic.Code != "42P01" {
		return err
	}
	copy := *diagnostic
	copy.Detail = fmt.Sprintf(`There is a WITH item named "%s", but it cannot be referenced from this part of the query.`, name)
	copy.Hint = "Use WITH RECURSIVE, or re-order the WITH items to remove forward references."
	return &copy
}
