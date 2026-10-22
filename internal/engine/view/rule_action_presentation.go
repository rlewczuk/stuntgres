package view

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// freezeRuleActionPresentation freezes the presentation of one rule action
// read through catalogView, with owner's temporary objects.
func freezeRuleActionPresentation(catalogView catalog.Read, owner ids.SessionToken, action parser.Statement, b analyze.Binding) (*stored.View, error) {
	if query, ok := action.(*parser.Select); ok {
		return stored.FreezeQueryPresentation(catalogView, owner, query, b, false, false, 0, "", false)
	}
	if insert, ok := action.(*parser.Insert); ok && insert.Source != nil && b.Query != nil {
		query := insert.Source.Select
		if query == nil {
			query = &parser.Select{Values: insert.Source.Values}
			// VALUES binding owns a project without SELECT syntax. Retain its
			// complete envelope under the presentation's synthetic SELECT key.
			project := *b.Query.Root.(*plan.ProjectNode)
			project.Syntax = query
			producer := *b.Query
			producer.Root = &project
			b.Query = &producer
		}
		return stored.FreezeQueryPresentation(catalogView, owner, query, b, false, false, 0, "", false)
	}
	metadata := b
	query := &parser.Select{}
	switch syntax := action.(type) {
	case *parser.Update:
		query.From = syntax.From
	case *parser.Delete:
		query.From = syntax.Using
	}
	if syntax, ok := action.(*parser.Merge); ok {
		query.From = syntax.Source
	}
	var expressions []expr.Expr
	stored.VisitRuleActionExpressions(b, func(node expr.Expr) { expressions = append(expressions, node) })
	var from plan.Node
	if b.Update != nil {
		from = b.Update.From
	}
	if b.Delete.Using != nil {
		from = b.Delete.Using
	}
	if b.Merge != nil {
		from = b.Merge.Source
	}
	// Correlated mutation subqueries see the installed target row followed
	// by FROM/USING slots. Keep that lexical row in the presentation too;
	// it is not a second source occurrence or an executable join.
	if b.Relation != nil {
		target := &plan.SingletonNode{QuerySchema: b.Relation.Columns}
		if from == nil {
			from = target
		} else {
			schema := append(append(plan.QuerySchema(nil), target.Schema()...), from.Schema()...)
			from = &plan.JoinNode{QuerySchema: schema, Left: target, Right: from, LeftWidth: len(target.Schema()), RightWidth: len(from.Schema())}
		}
	} else if from == nil {
		from = &plan.SingletonNode{}
	}
	metadata.Query = &plan.Query{Root: &plan.ProjectNode{Child: from, Expressions: expressions, Syntax: query, Frame: 1}}
	metadata = RetainRuleCTEPresentation(catalogView, metadata)
	presentation, err := stored.FreezeQueryPresentation(catalogView, owner, query, metadata, false, false, 0, "", false)
	if err != nil {
		return nil, err
	}
	return presentation.With(func(p *stored.ViewParts) {
		p.Frames[1] = metadata.Query.Root.(*plan.ProjectNode)
		p.MutationFrame = b.Relation != nil
	}), nil
}

// freezeRuleModifierPresentations freezes the presentation of every
// data-modifying CTE of a rule action, keyed by its statement.
func freezeRuleModifierPresentations(catalogView catalog.Read, owner ids.SessionToken, b analyze.Binding) (map[parser.Statement]*stored.View, error) {
	var presentations map[parser.Statement]*stored.View
	var collect func(analyze.Binding) error
	collect = func(body analyze.Binding) error {
		for _, definition := range body.CTEs {
			mutation, ok := analyze.MutationBinding(definition)
			if !ok || presentations[definition.Statement] != nil {
				continue
			}
			// Auxiliary bindings share the outer statement binder's capture
			// tables. Those tables are published on the completed action binding,
			// not on each mutation. Borrow them for freezing while retaining the
			// modifier's own executable query and globally indexed source leaves.
			metadata := *mutation
			metadata.Dependencies = b.Dependencies
			metadata.Capture.Literals, metadata.Capture.CastUsages = b.Capture.Literals, b.Capture.CastUsages
			metadata.Capture.RoutineRefs = b.Capture.RoutineRefs
			metadata.Capture.OperatorRefs = b.Capture.OperatorRefs
			metadata.Capture.Composites = b.Capture.Composites
			metadata.Capture.SimilarSubstrings, metadata.Capture.PosixSubstrings = b.Capture.SimilarSubstrings, b.Capture.PosixSubstrings
			metadata.Capture.Collations = b.Capture.Collations
			metadata.Capture.Columns, metadata.Capture.Stars = b.Capture.Columns, b.Capture.Stars
			metadata.Capture.Namespaces = b.Capture.Namespaces
			metadata.Prepared.SequenceConstants, metadata.Prepared.RegtypeConstants = b.Prepared.SequenceConstants, b.Prepared.RegtypeConstants
			presentation, err := freezeRuleActionPresentation(catalogView, owner, definition.Statement, metadata)
			if err != nil {
				return err
			}
			if presentations == nil {
				presentations = make(map[parser.Statement]*stored.View)
			}
			presentations[definition.Statement] = presentation
			if err := collect(*mutation); err != nil {
				return err
			}
		}
		return nil
	}
	if err := collect(b); err != nil {
		return nil, err
	}
	return presentations, nil
}
