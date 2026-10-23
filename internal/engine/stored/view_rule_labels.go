package stored

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Recursive execution gives the anchor the CTE's declared output schema. The
// declaration already prints those names; recover the anchor's original target
// labels from the same typed naming authority as binding, including expansions.
func (v *QueryRenderer) ruleAnchorColumns(s *parser.Select, project *plan.ProjectNode) ([]catalog.Column, error) {
	columns := append([]catalog.Column(nil), project.Schema()...)
	slot := 0
	for target, raw := range s.Targets {
		path := analyze.ViewLiteralPath{Query: project.Frame, Clause: analyze.ViewTargetLiteral, Target: target}
		var names []string
		if _, star := raw.(parser.StarExpr); star {
			names = v.composites[path].Names
			if names == nil {
				for _, column := range v.stars[path] {
					name := v.ruleFrozenColumnName(column)
					if name == "" {
						return nil, fmt.Errorf("stuntgres: missing frozen recursive star label")
					}
					names = append(names, name)
				}
			}
		} else if field, expansion := raw.(parser.FieldExpr); expansion && field.Expansion {
			captured := v.composites[path]
			for _, attribute := range captured.Attributes {
				for i := 0; i < captured.Base.Type.CompositeLen(); i++ {
					member := captured.Base.Type.CompositeMember(i)
					if member.Attribute == attribute {
						names = append(names, member.Name)
						break
					}
				}
			}
		} else {
			if slot >= len(project.Expressions) {
				return nil, fmt.Errorf("stuntgres: missing recursive target expression")
			}
			bound := project.Expressions[slot]
			name, _ := analyze.ExpressionName(raw, bound.ResultUsage(), bound)
			if target < len(s.Labels) && s.Labels[target].Text != "" {
				name = s.Labels[target].Text
			}
			names = []string{name}
		}
		for _, name := range names {
			if slot >= len(columns) {
				return nil, fmt.Errorf("stuntgres: recursive target label width mismatch")
			}
			columns[slot].Name = name
			slot++
		}
	}
	if slot != len(columns) {
		return nil, fmt.Errorf("stuntgres: recursive target label width mismatch")
	}
	return columns, nil
}

func (v *QueryRenderer) ruleFrozenColumnName(column analyze.ViewColumn) string {
	if column.RuleSide == 0 {
		for _, namespace := range v.namespaces {
			if namespace.Frame == column.Frame {
				for _, candidate := range namespace.Columns {
					if candidate.Slot == column.Slot {
						return candidate.Name
					}
				}
			}
		}
		return ""
	}
	address := column.RuleRef.Address
	attribute := address.SubID
	address.SubID = 0
	for ref, descriptor := range v.references {
		if ref.Address != address {
			continue
		}
		if relation, ok := descriptor.(*catalog.Relation); ok {
			for i, candidate := range relation.Attributes {
				if candidate == attribute {
					return relation.Columns[i].Name
				}
			}
		}
	}
	return ""
}
