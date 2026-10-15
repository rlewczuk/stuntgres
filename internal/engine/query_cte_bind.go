package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// A definition is immutable after statement analysis. Only invocation owners
// contain producer state; neither catalog objects nor prepared handles do.
type cteDefinition struct {
	query      *BoundQuery
	statement  parser.Statement
	mutation   *binding
	schema     querySchema
	owner      uint32
	references int
	hint       parser.CTEMaterialization
	volatile   bool
	recursive  bool
	syntax     *parser.Select
	name       string
	columns    []parser.Identifier
	position   parser.Position
	cycle      *cycleRecipe
}
type cteBinding struct {
	name       string
	definition *cteDefinition
	contained  bool // This binding is inside its owning WITH's definition bodies.
}

func (d *cteDefinition) folded() bool {
	return !d.recursive && d.mutation == nil && !d.volatile && d.hint != parser.CTEMaterialized && (d.hint == parser.CTENotMaterialized || d.references == 1)
}

func (d *cteDefinition) outputSchema() querySchema {
	if d.query != nil {
		return d.query.Root.Schema()
	}
	return d.schema
}

func futureCTEError(err error, name string) error {
	var diagnostic *sqlstate.Error
	if name == "" || !errors.As(err, &diagnostic) || diagnostic.Code != "42P01" {
		return err
	}
	copy := *diagnostic
	copy.Detail = fmt.Sprintf(`There is a WITH item named "%s", but it cannot be referenced from this part of the query.`, name)
	copy.Hint = "Use WITH RECURSIVE, or re-order the WITH items to remove forward references."
	return &copy
}

func (b *expressionBinder) bindCTEDefinitions(rawDefinitions []parser.CommonTableExpression, recursive bool) ([]*cteDefinition, error) {
	analysis, err := parser.AnalyzeRecursiveCTEs(&parser.Select{With: rawDefinitions, WithRecursive: recursive})
	if err != nil {
		return nil, err
	}
	definitions := make([]*cteDefinition, len(rawDefinitions))
	startBindings := len(b.cteBindings)
	for _, index := range analysis.Order {
		raw := rawDefinitions[index]
		start := len(b.querySources)
		definition := &cteDefinition{owner: b.frameID(), hint: raw.Materialization, position: raw.Name.Pos, name: raw.Name.Text, columns: raw.Columns, recursive: analysis.Recursive[index]}
		if raw.Cycle != nil {
			definition.cycle, err = b.bindCycleMarks(raw.Cycle)
			if err != nil {
				return nil, err
			}
			if !definition.recursive {
				return nil, expressionError("42601", "WITH query is not recursive", raw.Name.Pos)
			}
		}
		if selection, ok := raw.Query.(*parser.Select); ok {
			b.cteSelectBodies = true
			if selection.Values != nil {
				return nil, expressionError("0A000", "WITH bodies must be SELECT queries", raw.Name.Pos)
			}
			b.cteAnalysisDepth++
			oldRecursive := b.recursiveDefinition
			if definition.recursive {
				definition.syntax = selection
				b.recursiveDefinition = definition
			}
			query, err := b.bindSelect(selection, false)
			b.cteAnalysisDepth--
			b.recursiveDefinition = oldRecursive
			if err != nil {
				return nil, err
			}
			for i := start; i < len(b.querySources); i++ {
				b.querySources[i].analysisOnly = true
			}
			viewVolatile, err := cteStoredViewVolatile(b.queryAuth.catalog, query.Root)
			if err != nil {
				return nil, err
			}
			definition.query = query
			definition.volatile = cteVolatile(query.Root) || viewVolatile
		} else {
			mutation, err := b.bindCTEMutation(raw.Query)
			if err != nil {
				return nil, err
			}
			definition.statement, definition.mutation = raw.Query, &mutation
			if mutation.returning != nil {
				definition.schema = mutation.returning.schema
			}
		}
		if len(raw.Columns) > len(definition.outputSchema()) {
			return nil, expressionError("42P10", fmt.Sprintf(`WITH query "%s" has %d columns available but %d columns specified`, raw.Name.Text, len(definition.outputSchema()), len(raw.Columns)), raw.Name.Pos)
		}
		if len(raw.Columns) != 0 {
			aliased := append(querySchema(nil), definition.outputSchema()...)
			for i, name := range raw.Columns {
				aliased[i].Name = name.Text
			}
			if definition.query != nil {
				project := *definition.query.Root.(*projectNode)
				project.querySchema = aliased
				definition.query.Root = &project
			} else {
				definition.schema = aliased
				definition.mutation.returning.schema = aliased
			}
		}
		definitions[index] = definition
		b.cteBindings = append(b.cteBindings, cteBinding{name: raw.Name.Text, definition: definition, contained: true})
	}
	// The owning WITH's main query is outside its definition bodies. Keep
	// outer bindings' lexical state intact for nested WITHs and subqueries.
	for i := startBindings; i < len(b.cteBindings); i++ {
		b.cteBindings[i].contained = false
	}
	return definitions, nil
}

func (b *expressionBinder) bindCTEScan(table *parser.TableReference, binding cteBinding) (LogicalNode, queryNamespace, error) {
	d := binding.definition
	if d.mutation != nil && d.mutation.returning == nil {
		return nil, queryNamespace{}, expressionError("0A000", fmt.Sprintf(`WITH query "%s" does not have a RETURNING clause`, table.Name.Parts[0].Text), table.Name.Parts[0].Pos)
	}
	d.references++
	schema := d.outputSchema()
	var scan *derivedNode
	var root LogicalNode
	if d.recursive && d.query == nil {
		self := &worktableScanNode{}
		scan, root = &self.derivedNode, self
		if b.recursiveFrames == nil {
			b.recursiveFrames = make(map[uint32]bool)
		}
		b.recursiveFrames[b.frameID()] = true
	} else {
		scan = &derivedNode{}
		root = scan
	}
	*scan = derivedNode{querySchema: schema, query: d.query, cte: d, table: table, ordinal: -1 - b.cteScanOrdinal, scanFrame: b.frameID(), demand: make([]bool, len(schema))}
	b.cteScanOrdinal++
	columns := make([]queryColumn, len(schema))
	for i, column := range schema {
		columns[i] = queryColumn{descriptor: column, slot: i, leaf: -1, physical: i, cteScan: scan, hideStar: d.cycle != nil && binding.contained && i >= d.cycle.width}
	}
	name := table.Name.Parts[0].Text
	qualifier := queryQualifier{name: name, original: name, columns: columns}
	b.queryHidden = append(b.queryHidden, qualifier)
	return root, queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: schema}, nil
}

// Runtime demand omits unused definitions, while PostgreSQL's static folding
// eligibility inspects even nested WITH bodies that will never execute.
func visitExecutableQuery(node LogicalNode, visit func(LogicalNode), expression func(BoundExpr)) {
	visitCTEQuery(node, visit, expression, false)
}

func visitCTEQuery(node LogicalNode, visit func(LogicalNode), expression func(BoundExpr), includeDefinitions bool) {
	if node == nil {
		return
	}
	visit(node)
	queryExpressions(node, func(expr BoundExpr) {
		if expr == nil {
			return
		}
		if edge, ok := expr.(subqueryExpression); ok && edge.cteDefinition && !includeDefinitions {
			return
		}
		expression(expr)
		walkBoundExpression(expr, func(n BoundExpr) {
			if s, ok := n.(subqueryExpression); ok {
				visitCTEQuery(s.queryPlan.Root, visit, expression, includeDefinitions)
			}
		})
	})
	left, right := queryInputs(node)
	visitCTEQuery(left, visit, expression, includeDefinitions)
	visitCTEQuery(right, visit, expression, includeDefinitions)
}

func cteVolatile(root LogicalNode) bool {
	volatile := false
	visitCTEQuery(root, func(n LogicalNode) {
		if source, ok := n.(*sourceNode); ok && source.ref.Address.ClassID == RelationClassID && source.ref.Address.ObjectID == 12048 {
			volatile = true
		}
		for _, mark := range queryRootRowMarks(n) {
			if mark.mode != 0 {
				volatile = true
			}
		}
	}, func(expr BoundExpr) {
		if expr.Properties().Volatility == Volatile {
			volatile = true
		}
	}, true)
	return volatile
}

// Stored views are source leaves, not logical inputs. Inspect their immutable
// definitions using the same identity-bound graph authority as view expansion;
// no source is opened and no expression is evaluated during this analysis.
func cteStoredViewVolatile(catalog CatalogRead, root LogicalNode) (bool, error) {
	var leaves []queryLeaf
	var sourceErr error
	visitCTEQuery(root, func(node LogicalNode) {
		if source, ok := node.(*sourceNode); ok {
			relation, _ := catalog.state.descriptors[source.ref.Address].(*relationDescriptor)
			if relation == nil {
				sourceErr = ErrCatalogReplaced
			} else if relation.relationKind == viewRelation {
				leaves = append(leaves, queryLeaf{relation: relation})
			}
		}
	}, func(BoundExpr) {}, true)
	if sourceErr != nil {
		return false, sourceErr
	}
	if len(leaves) == 0 {
		return false, nil
	}
	if _, err := validateViewReadGraph(catalog, leaves); err != nil {
		return false, err
	}
	// Validation above owns cycle and stored-reference errors. Memoization here
	// only avoids re-inspecting a shared acyclic definition in this analysis.
	memo := make(map[ObjectAddress]bool)
	var visit func(*relationDescriptor) bool
	visit = func(relation *relationDescriptor) bool {
		if relation.relationKind != viewRelation {
			return relation.relationKind == catalogRelation && relation.address.ClassID == RelationClassID && relation.address.ObjectID == 12048
		}
		if volatile, ok := memo[relation.address]; ok {
			return volatile
		}
		volatile := cteVolatile(relation.view.query.Root)
		for _, source := range relation.view.sources {
			if volatile {
				break
			}
			child := catalog.state.descriptors[source.ref.Address].(*relationDescriptor)
			volatile = visit(child) || volatile
		}
		memo[relation.address] = volatile
		return volatile
	}
	for _, leaf := range leaves {
		if visit(leaf.relation) {
			return true, nil
		}
	}
	return false, nil
}

func activateCTESources(root LogicalNode, leaves []queryLeaf) {
	visitExecutableQuery(root, func(n LogicalNode) {
		if source, ok := n.(*sourceNode); ok && source.occurrence >= 0 && source.occurrence < len(leaves) {
			leaves[source.occurrence].analysisOnly = false
		}
	}, func(BoundExpr) {})
}
