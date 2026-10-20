package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

type cteBinding struct {
	name       string
	definition *plan.CTEDefinition
	contained  bool // This binding is inside its owning WITH's definition bodies.
}

func (b *expressionBinder) bindCTEDefinitions(rawDefinitions []parser.CommonTableExpression, recursive bool) ([]*plan.CTEDefinition, error) {
	analysis, err := parser.AnalyzeRecursiveCTEs(&parser.Select{With: rawDefinitions, WithRecursive: recursive})
	if err != nil {
		return nil, err
	}
	definitions := make([]*plan.CTEDefinition, len(rawDefinitions))
	startBindings := len(b.cte.bindings)
	for _, index := range analysis.Order {
		raw := rawDefinitions[index]
		start := len(b.query.sources)
		definition := &plan.CTEDefinition{Owner: b.FrameID(), Hint: raw.Materialization, Position: raw.Name.Pos, Name: raw.Name.Text, Columns: raw.Columns, Recursive: analysis.Recursive[index]}
		if raw.Search != nil {
			definition.Search = &plan.SearchRecipe{Syntax: raw.Search}
		}
		if raw.Cycle != nil {
			definition.Cycle, err = b.bindCycleMarks(raw.Cycle)
			if err != nil {
				return nil, err
			}
			if !definition.Recursive {
				return nil, expr.ErrorAt("42601", "WITH query is not recursive", raw.Name.Pos)
			}
		}
		if selection, ok := raw.Query.(*parser.Select); ok {
			b.cte.selectBodies = true
			if selection.Values != nil {
				return nil, expr.ErrorAt("0A000", "WITH bodies must be SELECT queries", raw.Name.Pos)
			}
			b.cte.analysisDepth++
			oldRecursive := b.cte.recursiveDefinition
			if definition.Recursive {
				definition.Syntax = selection
				b.cte.recursiveDefinition = definition
			}
			query, err := b.BindSelect(selection, false)
			b.cte.analysisDepth--
			b.cte.recursiveDefinition = oldRecursive
			if err != nil {
				return nil, err
			}
			for i := start; i < len(b.query.sources); i++ {
				b.query.sources[i].AnalysisOnly = true
			}
			viewVolatile, err := b.host.StoredViewVolatile(b.query.auth, query.Root)
			if err != nil {
				return nil, err
			}
			definition.Query = query
			definition.Volatile = analyze.CTEVolatile(query.Root) || viewVolatile
		} else {
			mutation, err := b.bindCTEMutation(raw.Query)
			if err != nil {
				return nil, err
			}
			definition.Statement, definition.Mutation = raw.Query, &mutation
			if mutation.Returning != nil {
				definition.Schema = mutation.Returning.Schema
			}
		}
		if len(raw.Columns) > len(definition.OutputSchema()) {
			return nil, expr.ErrorAt("42P10", fmt.Sprintf(`WITH query "%s" has %d columns available but %d columns specified`, raw.Name.Text, len(definition.OutputSchema()), len(raw.Columns)), raw.Name.Pos)
		}
		if raw.Search != nil && !definition.Recursive {
			return nil, expr.ErrorAt("42601", "WITH query is not recursive", raw.Name.Pos)
		}
		if len(raw.Columns) != 0 {
			aliased := append(plan.QuerySchema(nil), definition.OutputSchema()...)
			for i, name := range raw.Columns {
				aliased[i].Name = name.Text
			}
			if definition.Query != nil {
				project := *definition.Query.Root.(*plan.ProjectNode)
				project.QuerySchema = aliased
				definition.Query.Root = &project
			} else {
				definition.Schema = aliased
				mutation, _ := analyze.MutationBinding(definition)
				mutation.Returning.Schema = aliased
			}
		}
		definitions[index] = definition
		b.cte.bindings = append(b.cte.bindings, cteBinding{name: raw.Name.Text, definition: definition, contained: true})
	}
	// The owning WITH's main query is outside its definition bodies. Keep
	// outer bindings' lexical state intact for nested WITHs and subqueries.
	for i := startBindings; i < len(b.cte.bindings); i++ {
		b.cte.bindings[i].contained = false
	}
	return definitions, nil
}

func (b *expressionBinder) bindCTEScan(table *parser.TableReference, binding cteBinding) (plan.Node, queryNamespace, error) {
	d := binding.definition
	if mutation, ok := analyze.MutationBinding(d); ok && mutation.Returning == nil {
		return nil, queryNamespace{}, expr.ErrorAt("0A000", fmt.Sprintf(`WITH query "%s" does not have a RETURNING clause`, table.Name.Parts[0].Text), table.Name.Parts[0].Pos)
	}
	d.References++
	schema := d.OutputSchema()
	var scan *plan.DerivedNode
	var root plan.Node
	if d.Recursive && d.Query == nil {
		self := &plan.WorktableScanNode{}
		scan, root = &self.DerivedNode, self
		if b.cte.recursiveFrames == nil {
			b.cte.recursiveFrames = make(map[uint32]bool)
		}
		b.cte.recursiveFrames[b.FrameID()] = true
	} else {
		scan = &plan.DerivedNode{}
		root = scan
	}
	*scan = plan.DerivedNode{QuerySchema: schema, Query: d.Query, CTE: d, Table: table, Ordinal: -1 - b.cte.scanOrdinal, ScanFrame: b.FrameID(), Demand: make([]bool, len(schema))}
	b.cte.scanOrdinal++
	columns := make([]queryColumn, len(schema))
	for i, column := range schema {
		columns[i] = queryColumn{Descriptor: column, Slot: i, Leaf: -1, Physical: i, CTEScan: scan, HideStar: (d.Search != nil || d.Cycle != nil) && binding.contained && i >= d.UserWidth}
	}
	name := table.Name.Parts[0].Text
	qualifier := queryQualifier{Name: name, Original: name, Columns: columns}
	b.query.hidden = append(b.query.hidden, qualifier)
	return root, queryNamespace{Columns: columns, Qualifiers: []queryQualifier{qualifier}, Frame: schema}, nil
}

func activateCTESources(root plan.Node, leaves []exec.Leaf) {
	plan.VisitExecutableQuery(root, func(n plan.Node) {
		if source, ok := n.(*plan.SourceNode); ok && source.Occurrence >= 0 && source.Occurrence < len(leaves) {
			leaves[source.Occurrence].AnalysisOnly = false
		}
	}, func(expr.Expr) {})
}
