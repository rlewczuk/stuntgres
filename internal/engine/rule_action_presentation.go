package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func (e *Engine) freezeRuleActionPresentationLocked(auth AuthorizationView, action parser.Statement, b analyze.Binding) (*viewDefinition, error) {
	if query, ok := action.(*parser.Select); ok {
		return freezeQueryPresentation(auth.Catalog, catalogSourceTemporaryOwner(auth), query, b, false, false, 0, "", false)
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
		return freezeQueryPresentation(auth.Catalog, catalogSourceTemporaryOwner(auth), query, b, false, false, 0, "", false)
	}
	metadata := b
	query := &parser.Select{}
	switch syntax := action.(type) {
	case *parser.Update:
		query.From = syntax.From
	case *parser.Delete:
		query.From = syntax.Using
	}
	var expressions []expr.Expr
	for _, row := range b.Expressions {
		expressions = append(expressions, row...)
	}
	var from plan.Node
	if b.Update != nil {
		from = b.Update.From
		expressions = append(expressions, b.Update.Assignments...)
		if b.Update.Predicate != nil {
			expressions = append(expressions, b.Update.Predicate)
		}
	}
	if b.Delete.Using != nil {
		from = b.Delete.Using
	}
	if b.Predicate != nil {
		expressions = append(expressions, b.Predicate)
	}
	if b.Returning != nil {
		expressions = append(expressions, b.Returning.Expressions...)
	}
	if from == nil {
		from = &plan.SingletonNode{}
	}
	metadata.Query = &plan.Query{Root: &plan.ProjectNode{Child: from, Expressions: expressions, Syntax: query, Frame: 1}}
	metadata = retainRuleCTEPresentation(auth.Catalog, metadata)
	presentation, err := freezeQueryPresentation(auth.Catalog, catalogSourceTemporaryOwner(auth), query, metadata, false, false, 0, "", false)
	if err != nil {
		return nil, err
	}
	return presentation.With(func(p *ViewParts) { p.Frames[1] = metadata.Query.Root.(*plan.ProjectNode) }), nil
}

func (e *Engine) freezeRuleModifierPresentationsLocked(auth AuthorizationView, b analyze.Binding) (map[parser.Statement]*viewDefinition, error) {
	var presentations map[parser.Statement]*viewDefinition
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
			presentation, err := e.freezeRuleActionPresentationLocked(auth, definition.Statement, metadata)
			if err != nil {
				return err
			}
			if presentations == nil {
				presentations = make(map[parser.Statement]*viewDefinition)
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
