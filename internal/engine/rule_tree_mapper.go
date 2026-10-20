package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
)

type ruleTreeMapper struct {
	query      func(LogicalNode) (LogicalNode, error)
	expression func(expr.Expr) (expr.Expr, bool, error)
	definition func(*cteDefinition) (*cteDefinition, error)
}

type ruleTreeMapping struct {
	ownerFrame func(uint32) uint32
	leaf       func(expr.Expr) (expr.Expr, bool, error)
	boundary   func(LogicalNode) (LogicalNode, bool)
	finish     func(LogicalNode) (LogicalNode, error)
	mutation   func(*cteDefinition) (binding, error)
}

// One memo owns the entire binding: every action-local provider and its scans
// share one mapped definition/project. Protected statement providers stay exact.
func newRuleTreeMapper(view catalog.Read, frame func(uint32) uint32, offset int, retained []*cteDefinition, options ...ruleTreeMapping) ruleTreeMapper {
	if frame == nil {
		frame = func(value uint32) uint32 { return value }
	}
	var mapping ruleTreeMapping
	if len(options) != 0 {
		mapping = options[0]
	}
	ownerFrame := frame
	if mapping.ownerFrame != nil {
		ownerFrame = mapping.ownerFrame
	}
	var mapper ruleTreeMapper
	var definitions map[*cteDefinition]*cteDefinition
	var nodes map[LogicalNode]LogicalNode
	protected := func(definition *cteDefinition) bool {
		return definition != nil && (slices.Contains(retained, definition) || definition.Mutation != nil && mapping.mutation == nil)
	}
	mapper.definition = func(original *cteDefinition) (*cteDefinition, error) {
		if original == nil || protected(original) {
			return original, nil
		}
		if mapped := definitions[original]; mapped != nil {
			return mapped, nil
		}
		copy := *original
		copy.Owner = ownerFrame(original.Owner)
		if definitions == nil {
			definitions = make(map[*cteDefinition]*cteDefinition)
		}
		definitions[original] = &copy
		if original.Query != nil {
			root, err := mapper.query(original.Query.Root)
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
			body, err := mapping.mutation(original)
			if err != nil {
				return nil, err
			}
			body, err = mapRuleBindingRecipes(body, mapper)
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
	mapper.expression = func(bound expr.Expr) (expr.Expr, bool, error) {
		return rewriteViewExpression(view, bound, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
			if mapping.leaf != nil {
				next, changed, err := mapping.leaf(expr)
				if changed || err != nil {
					return next, changed, err
				}
			}
			switch node := expr.(type) {
			case columnExpression:
				if node.Scope == rulePseudoScope {
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
			case aggregateExpression:
				next := frame(node.Frame)
				if next != node.Frame {
					node.Frame = next
					return node, true, nil
				}
			case groupExpression:
				next := frame(node.Frame)
				if next != node.Frame {
					node.Frame = next
					return node, true, nil
				}
			case groupingExpression:
				next := frame(node.Frame)
				if next != node.Frame {
					node.Frame = next
					return node, true, nil
				}
			case returningWholeRowExpression:
				next := frame(node.Frame)
				if next != node.Frame {
					node.Frame = next
					return node, true, nil
				}
			case *windowExpression:
				next := frame(node.Frame)
				if next != node.Frame {
					copy := *node
					copy.Frame = next
					return &copy, true, nil
				}
			}
			return expr, false, nil
		}, func(root LogicalNode, _ func(expr.Expr) (expr.Expr, bool, error)) (LogicalNode, error) {
			return mapper.query(root)
		})
	}
	var rewriteNode func(LogicalNode) (LogicalNode, error)
	rewriteNode = func(original LogicalNode) (LogicalNode, error) {
		if mapping.boundary != nil {
			if next, opaque := mapping.boundary(original); opaque {
				return next, nil
			}
		}
		if mapped := nodes[original]; mapped != nil {
			if mapped == original && mapping.finish != nil {
				if project, ok := mapped.(*projectNode); ok {
					copy := *project
					return &copy, nil
				}
			}
			return mapped, nil
		}
		mapped := original
		switch node := original.(type) {
		case *sourceNode:
			if offset != 0 {
				copy := *node
				copy.Occurrence += offset
				mapped = &copy
			}
		case *projectNode:
			next := frame(node.Frame)
			if node.CTEOwner {
				next = ownerFrame(node.Frame)
			}
			if next != node.Frame {
				copy := *node
				copy.Frame = next
				mapped = &copy
			}
		case *joinNode:
			copy := *node
			changed := false
			if next := frame(node.Frame); next != node.Frame {
				copy.Frame = next
				changed = true
			}
			for i, capture := range node.Captures {
				if next := frame(capture.Frame); next != capture.Frame {
					if len(copy.Captures) != 0 && &copy.Captures[0] == &node.Captures[0] {
						copy.Captures = slices.Clone(node.Captures)
					}
					copy.Captures[i].Frame = next
					changed = true
				}
			}
			if changed {
				mapped = &copy
			}
		case *functionFromNode:
			copy := *node
			changed := false
			if next := frame(node.Frame); next != node.Frame {
				copy.Frame = next
				changed = true
			}
			for i, capture := range node.Captures {
				if next := frame(capture.Frame); next != capture.Frame {
					if len(copy.Captures) != 0 && &copy.Captures[0] == &node.Captures[0] {
						copy.Captures = slices.Clone(node.Captures)
					}
					copy.Captures[i].Frame = next
					changed = true
				}
			}
			if changed {
				mapped = &copy
			}
		case *setProjectNode:
			if next := frame(node.Frame); next != node.Frame {
				copy := *node
				copy.Frame = next
				mapped = &copy
			}
		case *derivedNode:
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
				copy.CTE, err = mapper.definition(node.CTE)
				if err != nil {
					return nil, err
				}
				if node.Query == node.CTE.Query {
					copy.Query = copy.CTE.Query
				}
			}
			if node.Query != nil && (node.CTE == nil || node.Query != node.CTE.Query) {
				root, err := mapper.query(node.Query.Root)
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
				nodes = make(map[LogicalNode]LogicalNode)
			}
			nodes[original] = &copy
			return &copy, nil
		case *worktableScanNode:
			copy := *node
			copy.ScanFrame = frame(node.ScanFrame)
			var err error
			copy.CTE, err = mapper.definition(node.CTE)
			if err != nil {
				return nil, err
			}
			mapped = &copy
		case *recursiveUnionNode:
			copy := *node
			var err error
			copy.Definition, err = mapper.definition(node.Definition)
			if err != nil {
				return nil, err
			}
			mapped = &copy
		}
		if mapped == original {
			return original, nil
		}
		result, err := rewriteQueryExpressions(mapped, mapper.expression, func(child LogicalNode) (LogicalNode, error) {
			if child == mapped {
				return child, nil
			}
			return rewriteNode(child)
		})
		if err != nil {
			return nil, err
		}
		if nodes == nil {
			nodes = make(map[LogicalNode]LogicalNode)
		}
		nodes[original] = result
		return result, nil
	}
	mapper.query = func(root LogicalNode) (LogicalNode, error) {
		if root == nil {
			return nil, nil
		}
		if mapped := nodes[root]; mapped != nil {
			return mapped, nil
		}
		mapped, err := rewriteQueryExpressions(root, mapper.expression, rewriteNode)
		if err != nil {
			return nil, err
		}
		if mapped != root && mapping.finish != nil {
			mapped, err = mapping.finish(mapped)
			if err != nil {
				return nil, err
			}
		}
		if nodes == nil {
			nodes = make(map[LogicalNode]LogicalNode)
		}
		nodes[root] = mapped
		if mapping.finish != nil {
			if _, project := mapped.(*projectNode); project {
				nodes[mapped] = mapped
			}
		}
		return mapped, nil
	}
	return mapper
}
