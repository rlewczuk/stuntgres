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
	owner      uint32
	references int
	hint       parser.CTEMaterialization
	volatile   bool
	position   parser.Position
}
type cteBinding struct {
	name       string
	definition *cteDefinition
}

func (d *cteDefinition) folded() bool {
	return !d.volatile && d.hint != parser.CTEMaterialized && (d.hint == parser.CTENotMaterialized || d.references == 1)
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

func (b *expressionBinder) bindCTEDefinitions(s *parser.Select) ([]*cteDefinition, error) {
	var definitions []*cteDefinition
	for _, raw := range s.With {
		if raw.Query.Values != nil {
			return nil, expressionError("0A000", "WITH bodies must be SELECT queries", raw.Name.Pos)
		}
		start := len(b.querySources)
		b.cteAnalysisDepth++
		query, err := b.bindSelect(raw.Query, false)
		b.cteAnalysisDepth--
		if err != nil {
			return nil, err
		}
		for i := start; i < len(b.querySources); i++ {
			b.querySources[i].analysisOnly = true
		}
		if len(raw.Columns) > len(query.Root.Schema()) {
			return nil, expressionError("42P10", fmt.Sprintf(`WITH query "%s" has %d columns available but %d columns specified`, raw.Name.Text, len(query.Root.Schema()), len(raw.Columns)), raw.Name.Pos)
		}
		definition := &cteDefinition{query: query, owner: b.frameID(), hint: raw.Materialization, position: raw.Name.Pos, volatile: cteVolatile(query.Root)}
		// Prefix aliases belong to the virtual output schema, not a catalog row.
		if len(raw.Columns) != 0 {
			project := *query.Root.(*projectNode)
			project.querySchema = append(querySchema(nil), project.querySchema...)
			for i, name := range raw.Columns {
				project.querySchema[i].Name = name.Text
			}
			query.Root = &project
		}
		definitions = append(definitions, definition)
		b.cteBindings = append(b.cteBindings, cteBinding{raw.Name.Text, definition})
	}
	return definitions, nil
}

func (b *expressionBinder) bindCTEScan(table *parser.TableReference, d *cteDefinition) (LogicalNode, queryNamespace, error) {
	d.references++
	schema := d.query.Root.Schema()
	scan := &derivedNode{querySchema: schema, query: d.query, cte: d, table: table, ordinal: -1 - b.cteScanOrdinal, scanFrame: b.frameID(), demand: make([]bool, len(schema))}
	b.cteScanOrdinal++
	columns := make([]queryColumn, len(schema))
	for i, column := range schema {
		columns[i] = queryColumn{descriptor: column, slot: i, leaf: -1, physical: i, cteScan: scan}
	}
	name := table.Name.Parts[0].Text
	qualifier := queryQualifier{name: name, original: name, columns: columns}
	b.queryHidden = append(b.queryHidden, qualifier)
	return scan, queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: schema}, nil
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

func activateCTESources(root LogicalNode, leaves []queryLeaf) {
	visitExecutableQuery(root, func(n LogicalNode) {
		if source, ok := n.(*sourceNode); ok && source.occurrence >= 0 && source.occurrence < len(leaves) {
			leaves[source.occurrence].analysisOnly = false
		}
	}, func(BoundExpr) {})
}
