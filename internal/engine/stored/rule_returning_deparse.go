package stored

import (
	"strings"

	"stuntgres/internal/parser"
)

func (a ruleRendering) returningSyntax() *parser.Returning {
	switch s := a.syntax.(type) {
	case *parser.Insert:
		return s.Returning
	case *parser.Update:
		return s.Returning
	case *parser.Delete:
		return s.Returning
	}
	return nil
}

func (a ruleRendering) returningAlias(old bool) string {
	name := "new"
	if old {
		name = "old"
	}
	if returning := a.returningSyntax(); returning != nil {
		for _, alias := range returning.Aliases {
			if alias.New != old {
				return alias.Alias.Text
			}
		}
	}
	return name
}

// Default-side columns share the executable target slot. Preserve their lexical
// side qualifier from the retained typed occurrence, never from SQL text. Other
// columns still use their bound source/target identity and current attributes.
func (a *ruleRendering) prepareReturning() (string, error) {
	if !a.policy.Rule {
		return "", nil
	}
	a.returning = true
	old, new := a.returningAlias(true), a.returningAlias(false)
	a.returningNames = make(map[parser.Position]string)
	err := parser.VisitQueryExpressions(a.syntax, func(raw parser.Expr) error {
		var qualifier string
		switch raw := raw.(type) {
		case parser.ColumnExpr:
			if len(raw.Name.Parts) == 2 {
				qualifier = raw.Name.Parts[0].Text
			}
		case parser.StarExpr:
			if len(raw.Qualifier.Parts) == 1 {
				qualifier = raw.Qualifier.Parts[0].Text
			}
		}
		if qualifier == old || qualifier == new {
			a.returningNames[raw.SourcePosition()] = qualifier
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	var options []string
	if old != "old" {
		options = append(options, "OLD AS "+a.policy.identifier(old))
	}
	if new != "new" {
		options = append(options, "NEW AS "+a.policy.identifier(new))
	}
	if len(options) == 0 {
		return "", nil
	}
	return "WITH (" + strings.Join(options, ", ") + ") ", nil
}
