package engine

import "slices"

type ruleTreeMapper struct {
	query      func(LogicalNode) (LogicalNode, error)
	expression func(BoundExpr) (BoundExpr, bool, error)
	definition func(*cteDefinition) (*cteDefinition, error)
}

type ruleTreeMapping struct {
	ownerFrame func(uint32) uint32
	leaf       func(BoundExpr) (BoundExpr, bool, error)
	boundary   func(LogicalNode) (LogicalNode, bool)
	finish     func(LogicalNode) (LogicalNode, error)
	mutation   func(*cteDefinition) (binding, error)
}

// One memo owns the entire binding: every action-local provider and its scans
// share one mapped definition/project. Protected statement providers stay exact.
func newRuleTreeMapper(view CatalogRead, frame func(uint32) uint32, offset int, retained []*cteDefinition, options ...ruleTreeMapping) ruleTreeMapper {
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
		return definition != nil && (slices.Contains(retained, definition) || definition.mutation != nil && mapping.mutation == nil)
	}
	mapper.definition = func(original *cteDefinition) (*cteDefinition, error) {
		if original == nil || protected(original) {
			return original, nil
		}
		if mapped := definitions[original]; mapped != nil {
			return mapped, nil
		}
		copy := *original
		copy.owner = ownerFrame(original.owner)
		if definitions == nil {
			definitions = make(map[*cteDefinition]*cteDefinition)
		}
		definitions[original] = &copy
		if original.query != nil {
			root, err := mapper.query(original.query.Root)
			if err != nil {
				return nil, err
			}
			if root != original.query.Root {
				query := *original.query
				query.Root = root
				copy.query = &query
			}
		}
		if original.mutation != nil {
			body, err := mapping.mutation(original)
			if err != nil {
				return nil, err
			}
			body, err = mapRuleBindingRecipes(body, mapper)
			if err != nil {
				return nil, err
			}
			copy.mutation = &body
		}
		if copy.owner == original.owner && copy.query == original.query && copy.mutation == original.mutation {
			definitions[original] = original
			return original, nil
		}
		return &copy, nil
	}
	mapper.expression = func(bound BoundExpr) (BoundExpr, bool, error) {
		return rewriteViewExpression(view, bound, nil, func(expr BoundExpr) (BoundExpr, bool, error) {
			if mapping.leaf != nil {
				next, changed, err := mapping.leaf(expr)
				if changed || err != nil {
					return next, changed, err
				}
			}
			switch node := expr.(type) {
			case columnExpression:
				if node.scope == rulePseudoScope {
					return expr, false, nil
				}
				changed := false
				if node.frame != 0 {
					next := frame(node.frame)
					changed = next != node.frame
					node.frame = next
				}
				if offset != 0 && node.queryOccurrence != 0 {
					node.queryOccurrence += offset
					changed = true
				}
				return node, changed, nil
			case aggregateExpression:
				next := frame(node.frame)
				if next != node.frame {
					node.frame = next
					return node, true, nil
				}
			case groupExpression:
				next := frame(node.frame)
				if next != node.frame {
					node.frame = next
					return node, true, nil
				}
			case groupingExpression:
				next := frame(node.frame)
				if next != node.frame {
					node.frame = next
					return node, true, nil
				}
			case returningWholeRowExpression:
				next := frame(node.frame)
				if next != node.frame {
					node.frame = next
					return node, true, nil
				}
			case *windowExpression:
				next := frame(node.frame)
				if next != node.frame {
					copy := *node
					copy.frame = next
					return &copy, true, nil
				}
			}
			return expr, false, nil
		}, func(root LogicalNode, _ func(BoundExpr) (BoundExpr, bool, error)) (LogicalNode, error) {
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
				copy.occurrence += offset
				mapped = &copy
			}
		case *projectNode:
			next := frame(node.frame)
			if node.cteOwner {
				next = ownerFrame(node.frame)
			}
			if next != node.frame {
				copy := *node
				copy.frame = next
				mapped = &copy
			}
		case *joinNode:
			copy := *node
			changed := false
			if next := frame(node.frame); next != node.frame {
				copy.frame = next
				changed = true
			}
			for i, capture := range node.captures {
				if next := frame(capture.frame); next != capture.frame {
					if len(copy.captures) != 0 && &copy.captures[0] == &node.captures[0] {
						copy.captures = slices.Clone(node.captures)
					}
					copy.captures[i].frame = next
					changed = true
				}
			}
			if changed {
				mapped = &copy
			}
		case *functionFromNode:
			copy := *node
			changed := false
			if next := frame(node.frame); next != node.frame {
				copy.frame = next
				changed = true
			}
			for i, capture := range node.captures {
				if next := frame(capture.frame); next != capture.frame {
					if len(copy.captures) != 0 && &copy.captures[0] == &node.captures[0] {
						copy.captures = slices.Clone(node.captures)
					}
					copy.captures[i].frame = next
					changed = true
				}
			}
			if changed {
				mapped = &copy
			}
		case *setProjectNode:
			if next := frame(node.frame); next != node.frame {
				copy := *node
				copy.frame = next
				mapped = &copy
			}
		case *derivedNode:
			copy := *node
			copy.scanFrame = frame(node.scanFrame)
			if protected(node.cte) {
				if copy.scanFrame == node.scanFrame && node.query == nil {
					return node, nil
				}
				return &copy, nil
			}
			if node.cte != nil {
				var err error
				copy.cte, err = mapper.definition(node.cte)
				if err != nil {
					return nil, err
				}
				if node.query == node.cte.query {
					copy.query = copy.cte.query
				}
			}
			if node.query != nil && (node.cte == nil || node.query != node.cte.query) {
				root, err := mapper.query(node.query.Root)
				if err != nil {
					return nil, err
				}
				if root != node.query.Root {
					query := *node.query
					query.Root = root
					copy.query = &query
				}
			}
			if copy.scanFrame == node.scanFrame && copy.cte == node.cte && copy.query == node.query && node.query == nil {
				return node, nil
			}
			if nodes == nil {
				nodes = make(map[LogicalNode]LogicalNode)
			}
			nodes[original] = &copy
			return &copy, nil
		case *worktableScanNode:
			copy := *node
			copy.scanFrame = frame(node.scanFrame)
			var err error
			copy.cte, err = mapper.definition(node.cte)
			if err != nil {
				return nil, err
			}
			mapped = &copy
		case *recursiveUnionNode:
			copy := *node
			var err error
			copy.definition, err = mapper.definition(node.definition)
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
