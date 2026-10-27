package parser

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
)

// RecursiveCTEAnalysis is statement-local analysis, never a runtime cache.
// Order indexes original definitions; Recursive distinguishes true self edges.
type RecursiveCTEAnalysis struct {
	Order     []int
	Recursive []bool
}

func recursiveCTEError(code, message string, pos Position) error {
	position := ""
	if pos.Character > 0 {
		position = strconv.Itoa(pos.Character)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
}

func AnalyzeRecursiveCTEs(statement Statement) (RecursiveCTEAnalysis, error) {
	if explanation, ok := statement.(*Explain); ok {
		statement = explanation.Statement
	}
	definitions := StatementWith(statement)
	result := RecursiveCTEAnalysis{Order: make([]int, 0, len(definitions)), Recursive: make([]bool, len(definitions))}
	if _, ok := statement.(*Merge); ok && StatementWithRecursive(statement) {
		return result, recursiveCTEError("42601", "WITH RECURSIVE is not supported for MERGE statement", Position{})
	}
	for i := range definitions {
		for j := range i {
			if definitions[i].Name.Text == definitions[j].Name.Text {
				return result, recursiveCTEError("42712", fmt.Sprintf(`WITH query name "%s" specified more than once`, definitions[i].Name.Text), definitions[i].Name.Pos)
			}
		}
	}
	if !StatementWithRecursive(statement) {
		for i := range definitions {
			result.Order = append(result.Order, i)
		}
		return result, nil
	}
	visible := make([]*CommonTableExpression, len(definitions))
	for i := range definitions {
		visible[i] = &definitions[i]
	}
	dependencies := make([][]int, len(definitions))
	for i := range definitions {
		err := visitQuerySources(definitions[i].Query, nil, nil, nil, func(_ *TableReference, target *CommonTableExpression, _ string) error {
			for j := range definitions {
				if target == &definitions[j] {
					if i == j {
						result.Recursive[i] = true
					} else {
						found := false
						for _, prior := range dependencies[i] {
							found = found || prior == j
						}
						if !found {
							dependencies[i] = append(dependencies[i], j)
						}
					}
					break
				}
			}
			return nil
		}, visible, "", false)
		if err != nil {
			return result, err
		}
	}
	state := make([]uint8, len(definitions))
	var sort func(int) error
	sort = func(i int) error {
		if state[i] == 2 {
			return nil
		}
		if state[i] == 1 {
			return recursiveCTEError("0A000", "mutual recursion between WITH items is not implemented", definitions[i].Name.Pos)
		}
		state[i] = 1
		for _, dependency := range dependencies[i] {
			if err := sort(dependency); err != nil {
				return err
			}
		}
		state[i] = 2
		result.Order = append(result.Order, i)
		return nil
	}
	for i := range definitions {
		if err := sort(i); err != nil {
			return result, err
		}
	}
	for _, i := range result.Order {
		if !result.Recursive[i] {
			continue
		}
		d := &definitions[i]
		if ModifyingStatement(d.Query) {
			return result, recursiveCTEError("42P19", fmt.Sprintf(`recursive query "%s" must not contain data-modifying statements`, d.Name.Text), d.Name.Pos)
		}
		body, ok := d.Query.(*Select)
		if !ok || body.Set == nil || body.Set.Operator != "union" {
			return result, recursiveCTEError("42P19", fmt.Sprintf(`recursive query "%s" does not have the form non-recursive-term UNION [ALL] recursive-term`, d.Name.Text), d.Name.Pos)
		}
		count := 0
		check := func(table *TableReference, target *CommonTableExpression, context string) error {
			if target != d {
				return nil
			}
			if context != "" {
				if context == "intersect" || context == "except" {
					context = strings.ToUpper(context)
				}
				return recursiveCTEError("42P19", fmt.Sprintf(`recursive reference to query "%s" must not appear within %s`, d.Name.Text, mapContext(context)), table.Name.Parts[0].Pos)
			}
			count++
			if count > 1 {
				return recursiveCTEError("42P19", fmt.Sprintf(`recursive reference to query "%s" must not appear more than once`, d.Name.Text), table.Name.Parts[0].Pos)
			}
			return nil
		}
		// PostgreSQL checks the body's WITH definition list in isolation before
		// decorations and arms. Do not expose this list's names as a namespace:
		// only nested WITH groups inside each definition body introduce shadows.
		for _, inner := range body.With {
			if err := visitQuerySources(inner.Query, nil, nil, nil, check, visible, "subquery", true); err != nil {
				return result, err
			}
		}
		if body.Order != nil && len(body.Order.Keys) > 0 {
			return result, recursiveCTEError("0A000", "ORDER BY in a recursive query is not implemented", body.Order.Keys[0].Expr.SourcePosition())
		}
		if body.Bounds != nil {
			if body.Bounds.Offset != nil {
				return result, recursiveCTEError("0A000", "OFFSET in a recursive query is not implemented", body.Bounds.Offset.SourcePosition())
			}
			if body.Bounds.Count != nil {
				return result, recursiveCTEError("0A000", "LIMIT in a recursive query is not implemented", body.Bounds.Count.SourcePosition())
			}
		}
		if len(body.Locking) > 0 {
			return result, recursiveCTEError("0A000", "FOR UPDATE/SHARE in a recursive query is not implemented", Position{})
		}
		if err := visitQuerySources(body.Set.Left, nil, nil, nil, check, visible, "non-recursive term", true); err != nil {
			return result, err
		}
		if err := visitQuerySources(body.Set.Right, nil, nil, nil, check, visible, "", true); err != nil {
			return result, err
		}
		// SEARCH checks belong after body analysis and name validation. Keep
		// the installed CYCLE-only legality boundary unchanged.
		if d.Search == nil && d.Cycle != nil {
			if err := CheckRecursiveCTESelectArms(body); err != nil {
				return result, err
			}
			if err := checkRecursiveCTETopLevel(body, d, visible); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

// CheckRecursiveCTESelectArms checks SEARCH/CYCLE expandability after the caller
// has analyzed the complete recursive body, before validating generated names.
func CheckRecursiveCTESelectArms(body *Select) error {
	if body.Set.Left.Set != nil {
		return recursiveCTEError("0A000", "with a SEARCH or CYCLE clause, the left side of the UNION must be a SELECT", Position{})
	}
	if body.Set.Right.Set != nil {
		return recursiveCTEError("42601", "with a SEARCH or CYCLE clause, the right side of the UNION must be a SELECT", Position{})
	}
	return nil
}

// CheckRecursiveCTETopLevel checks the rewrite restriction after SEARCH/CYCLE
// names. The existing source walker retains nested WITH shadowing and identity.
func CheckRecursiveCTETopLevel(body *Select, name Identifier) error {
	definition := &CommonTableExpression{Name: name, Query: body}
	return checkRecursiveCTETopLevel(body, definition, []*CommonTableExpression{definition})
}

func checkRecursiveCTETopLevel(body *Select, definition *CommonTableExpression, visible []*CommonTableExpression) error {
	top := make(map[*TableReference]bool)
	var collect func(FromItem)
	collect = func(item FromItem) {
		switch n := item.(type) {
		case *TableReference:
			top[n] = true
		case *AliasReference:
			collect(n.Input)
		case *JoinReference:
			collect(n.Left)
			collect(n.Right)
		}
	}
	collect(body.Set.Right.From)
	return visitQuerySources(body.Set.Right, nil, nil, nil, func(table *TableReference, target *CommonTableExpression, _ string) error {
		if target == definition && !top[table] {
			return recursiveCTEError("0A000", fmt.Sprintf(`with a SEARCH or CYCLE clause, the recursive reference to WITH query "%s" must be at the top level of its right-hand SELECT`, definition.Name.Text), Position{})
		}
		return nil
	}, visible, "", false)
}

func mapContext(context string) string {
	switch context {
	case "non-recursive term":
		return "its non-recursive term"
	case "subquery":
		return "a subquery"
	case "outer join":
		return "an outer join"
	}
	return context
}
