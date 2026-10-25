package stored

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

type ruleTreeMapper struct {
	Query      func(plan.Node) (plan.Node, error)
	Expression func(expr.Expr) (expr.Expr, bool, error)
	Definition func(*plan.CTEDefinition) (*plan.CTEDefinition, error)
}

type RuleTreeMapping struct {
	Recipe     expr.RoutineRecipeReference
	OwnerFrame func(uint32) uint32
	Leaf       func(expr.Expr) (expr.Expr, bool, error)
	Boundary   func(plan.Node) (plan.Node, bool)
	Finish     func(plan.Node) (plan.Node, error)
	Mutation   func(*plan.CTEDefinition) (analyze.Binding, error)
}

// One memo owns the entire binding: every action-local provider and its scans
// share one mapped definition/project. Protected statement providers stay exact.
func NewRuleTreeMapper(view catalog.Read, frame func(uint32) uint32, offset int, retained []*plan.CTEDefinition, options ...RuleTreeMapping) ruleTreeMapper {
	if frame == nil {
		frame = func(value uint32) uint32 { return value }
	}
	var mapping RuleTreeMapping
	if len(options) != 0 {
		mapping = options[0]
	}
	ownerFrame := frame
	if mapping.OwnerFrame != nil {
		ownerFrame = mapping.OwnerFrame
	}
	var mapper ruleTreeMapper
	var definitions map[*plan.CTEDefinition]*plan.CTEDefinition
	var nodes map[plan.Node]plan.Node
	protected := func(definition *plan.CTEDefinition) bool {
		return definition != nil && (slices.Contains(retained, definition) || definition.Mutation != nil && mapping.Mutation == nil)
	}
	mapper.Definition = func(original *plan.CTEDefinition) (*plan.CTEDefinition, error) {
		if original == nil || protected(original) {
			return original, nil
		}
		if mapped := definitions[original]; mapped != nil {
			return mapped, nil
		}
		copy := *original
		copy.Owner = ownerFrame(original.Owner)
		if definitions == nil {
			definitions = make(map[*plan.CTEDefinition]*plan.CTEDefinition)
		}
		definitions[original] = &copy
		if original.Query != nil {
			root, err := mapper.Query(original.Query.Root)
			if err != nil {
				return nil, err
			}
			if root != original.Query.Root {
				query := *original.Query
				query.Root = root
				copy.Query = &query
			}
		}
		if original.Mutation != nil {
			body, err := mapping.Mutation(original)
			if err != nil {
				return nil, err
			}
			body, err = MapRuleBindingRecipes(body, mapper)
			if err != nil {
				return nil, err
			}
			copy.Mutation = &body
		}
		if copy.Owner == original.Owner && copy.Query == original.Query && copy.Mutation == original.Mutation {
			definitions[original] = original
			return original, nil
		}
		return &copy, nil
	}
	mapper.Expression = func(bound expr.Expr) (expr.Expr, bool, error) {
		return RewriteViewExpression(view, bound, mapping.Recipe, func(expr expr.Expr) (expr.Expr, bool, error) {
			if mapping.Leaf != nil {
				next, changed, err := mapping.Leaf(expr)
				if changed || err != nil {
					return next, changed, err
				}
			}
			switch node := expr.(type) {
			case plan.TableOIDExpr:
				if node.Scope == 0 {
					return node, false, nil
				}
				next := frame(node.Frame)
				changed := next != node.Frame
				node.Frame = next
				if offset != 0 {
					node.QueryOccurrence += offset
					changed = true
				}
				return node, changed, nil
			case plan.ColumnExpr:
				if node.Scope == analyze.RulePseudoScope {
					return expr, false, nil
				}
				changed := false
				if node.Frame != 0 {
					next := frame(node.Frame)
					changed = next != node.Frame
					node.Frame = next
				}
				if offset != 0 && node.QueryOccurrence != 0 {
					node.QueryOccurrence += offset
					changed = true
				}
				return node, changed, nil
			case plan.AggregateExpr:
				next := frame(node.Frame)
				if next != node.Frame {
					node.Frame = next
					return node, true, nil
				}
			case plan.GroupExpr:
				next := frame(node.Frame)
				if next != node.Frame {
					node.Frame = next
					return node, true, nil
				}
			case plan.GroupingExpr:
				next := frame(node.Frame)
				if next != node.Frame {
					node.Frame = next
					return node, true, nil
				}
			case plan.ReturningWholeRowExpr:
				next := frame(node.Frame)
				if next != node.Frame {
					node.Frame = next
					return node, true, nil
				}
			case *plan.WindowExpr:
				next := frame(node.Frame)
				if next != node.Frame {
					copy := *node
					copy.Frame = next
					return &copy, true, nil
				}
			}
			return expr, false, nil
		}, func(root plan.Node, _ func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
			return mapper.Query(root)
		})
	}
	groupings := func(original []plan.Grouping) []plan.Grouping {
		if mapping.Recipe == nil || len(original) == 0 {
			return original
		}
		copy := slices.Clone(original)
		for i := range copy {
			if !copy[i].PrivateTree {
				copy[i].RoutineRecipe = mapping.Recipe
			}
		}
		return copy
	}
	order := func(original []plan.SortKey) []plan.SortKey {
		if mapping.Recipe == nil || len(original) == 0 {
			return original
		}
		var copy []plan.SortKey
		for i, key := range original {
			if key.EqualityProcedure != nil && key.EqualityRecipe != mapping.Recipe {
				if copy == nil {
					copy = slices.Clone(original)
				}
				copy[i].EqualityRecipe = mapping.Recipe
			}
		}
		if copy == nil {
			return original
		}
		return copy
	}
	var rewriteNode func(plan.Node) (plan.Node, error)
	rewriteNode = func(original plan.Node) (plan.Node, error) {
		if mapping.Boundary != nil {
			if next, opaque := mapping.Boundary(original); opaque {
				return next, nil
			}
		}
		if mapped := nodes[original]; mapped != nil {
			if mapped == original && mapping.Finish != nil {
				if project, ok := mapped.(*plan.ProjectNode); ok {
					copy := *project
					return &copy, nil
				}
			}
			return mapped, nil
		}
		mapped := original
		switch node := original.(type) {
		case *plan.AggregateNode:
			if mapping.Recipe != nil {
				copy := *node
				copy.Grouping = groupings(node.Grouping)
				copy.Calls = slices.Clone(node.Calls)
				for i, call := range copy.Calls {
					call.RoutineRecipe = mapping.Recipe
					call.Distinct = groupings(call.Distinct)
					call.Order = order(call.Order)
					copy.Calls[i] = call
				}
				mapped = &copy
			}
		case *plan.WindowNode:
			if mapping.Recipe != nil {
				copy := *node
				copy.Specs = slices.Clone(node.Specs)
				for i, spec := range copy.Specs {
					spec.Grouping = groupings(spec.Grouping)
					spec.Order = order(spec.Order)
					copy.Specs[i] = spec
				}
				copy.Calls = slices.Clone(node.Calls)
				for i, call := range copy.Calls {
					if call.Expression == nil {
						return nil, fmt.Errorf("stuntgres: missing stored window occurrence")
					}
					occurrence, _, err := mapper.Expression(call.Expression)
					if err != nil {
						return nil, err
					}
					copy.Calls[i].Expression = occurrence.(*plan.WindowExpr)
				}
				mapped = &copy
			}
		case *plan.DistinctNode:
			if mapping.Recipe != nil {
				copy := *node
				copy.Grouping = groupings(node.Grouping)
				mapped = &copy
			}
		case *plan.DistinctOnNode:
			if mapping.Recipe != nil {
				copy := *node
				copy.Grouping = groupings(node.Grouping)
				mapped = &copy
			}
		case *plan.SetNode:
			if mapping.Recipe != nil {
				copy := *node
				copy.Grouping = groupings(node.Grouping)
				mapped = &copy
			}
		case *plan.SortNode:
			if mapping.Recipe != nil {
				copy := *node
				copy.Keys = order(node.Keys)
				mapped = &copy
			}
		case *plan.SourceNode:
			if offset != 0 {
				copy := *node
				copy.Occurrence += offset
				mapped = &copy
			}
		case *plan.ProjectNode:
			next := frame(node.Frame)
			if node.CTEOwner {
				next = ownerFrame(node.Frame)
			}
			if next != node.Frame {
				copy := *node
				copy.Frame = next
				mapped = &copy
			}
		case *plan.JoinNode:
			copy := *node
			changed := false
			if next := frame(node.Frame); next != node.Frame {
				copy.Frame = next
				changed = true
			}
			for i, capture := range node.Captures {
				next, occurrence := frame(capture.Frame), capture.QueryOccurrence
				if occurrence != 0 {
					occurrence += offset
				}
				if next != capture.Frame || occurrence != capture.QueryOccurrence {
					if len(copy.Captures) != 0 && &copy.Captures[0] == &node.Captures[0] {
						copy.Captures = slices.Clone(node.Captures)
					}
					copy.Captures[i].Frame, copy.Captures[i].QueryOccurrence = next, occurrence
					changed = true
				}
			}
			if changed {
				mapped = &copy
			}
		case *plan.FunctionFromNode:
			copy := *node
			changed := false
			if next := frame(node.Frame); next != node.Frame {
				copy.Frame = next
				changed = true
			}
			for i, capture := range node.Captures {
				next, occurrence := frame(capture.Frame), capture.QueryOccurrence
				if occurrence != 0 {
					occurrence += offset
				}
				if next != capture.Frame || occurrence != capture.QueryOccurrence {
					if len(copy.Captures) != 0 && &copy.Captures[0] == &node.Captures[0] {
						copy.Captures = slices.Clone(node.Captures)
					}
					copy.Captures[i].Frame, copy.Captures[i].QueryOccurrence = next, occurrence
					changed = true
				}
			}
			if changed {
				mapped = &copy
			}
		case *plan.SetProjectNode:
			if next := frame(node.Frame); next != node.Frame {
				copy := *node
				copy.Frame = next
				mapped = &copy
			}
		case *plan.DerivedNode:
			copy := *node
			copy.ScanFrame = frame(node.ScanFrame)
			if protected(node.CTE) {
				if copy.ScanFrame == node.ScanFrame && node.Query == nil {
					return node, nil
				}
				return &copy, nil
			}
			if node.CTE != nil {
				var err error
				copy.CTE, err = mapper.Definition(node.CTE)
				if err != nil {
					return nil, err
				}
				if node.Query == node.CTE.Query {
					copy.Query = copy.CTE.Query
				}
			}
			if node.Query != nil && (node.CTE == nil || node.Query != node.CTE.Query) {
				root, err := mapper.Query(node.Query.Root)
				if err != nil {
					return nil, err
				}
				if root != node.Query.Root {
					query := *node.Query
					query.Root = root
					copy.Query = &query
				}
			}
			if copy.ScanFrame == node.ScanFrame && copy.CTE == node.CTE && copy.Query == node.Query && node.Query == nil {
				return node, nil
			}
			if nodes == nil {
				nodes = make(map[plan.Node]plan.Node)
			}
			nodes[original] = &copy
			return &copy, nil
		case *plan.WorktableScanNode:
			copy := *node
			copy.ScanFrame = frame(node.ScanFrame)
			var err error
			copy.CTE, err = mapper.Definition(node.CTE)
			if err != nil {
				return nil, err
			}
			mapped = &copy
		case *plan.RecursiveUnionNode:
			copy := *node
			copy.Grouping = groupings(node.Grouping)
			var err error
			copy.Definition, err = mapper.Definition(node.Definition)
			if err != nil {
				return nil, err
			}
			mapped = &copy
		}
		if mapped == original {
			return original, nil
		}
		result, err := plan.RewriteQueryExpressions(mapped, mapper.Expression, func(child plan.Node) (plan.Node, error) {
			if child == mapped {
				return child, nil
			}
			return rewriteNode(child)
		})
		if err != nil {
			return nil, err
		}
		if nodes == nil {
			nodes = make(map[plan.Node]plan.Node)
		}
		nodes[original] = result
		return result, nil
	}
	mapper.Query = func(root plan.Node) (plan.Node, error) {
		if root == nil {
			return nil, nil
		}
		if mapped := nodes[root]; mapped != nil {
			return mapped, nil
		}
		mapped, err := plan.RewriteQueryExpressions(root, mapper.Expression, rewriteNode)
		if err != nil {
			return nil, err
		}
		if mapped != root && mapping.Finish != nil {
			mapped, err = mapping.Finish(mapped)
			if err != nil {
				return nil, err
			}
		}
		if nodes == nil {
			nodes = make(map[plan.Node]plan.Node)
		}
		nodes[root] = mapped
		if mapping.Finish != nil {
			if _, project := mapped.(*plan.ProjectNode); project {
				nodes[mapped] = mapped
			}
		}
		return mapped, nil
	}
	return mapper
}
