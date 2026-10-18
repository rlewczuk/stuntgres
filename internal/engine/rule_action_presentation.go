package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func (e *Engine) freezeRuleActionPresentationLocked(auth AuthorizationView, action parser.Statement, b binding) (*viewDefinition, error) {
	if query, ok := action.(*parser.Select); ok {
		return e.freezeQueryPresentationLocked(auth, query, b, false, false, 0, "", false)
	}
	if insert, ok := action.(*parser.Insert); ok && insert.Source != nil && b.query != nil {
		query := insert.Source.Select
		if query == nil {
			query = &parser.Select{Values: insert.Source.Values}
			// VALUES binding owns a project without SELECT syntax. Retain its
			// complete envelope under the presentation's synthetic SELECT key.
			project := *b.query.Root.(*projectNode)
			project.syntax = query
			producer := *b.query
			producer.Root = &project
			b.query = &producer
		}
		return e.freezeQueryPresentationLocked(auth, query, b, false, false, 0, "", false)
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
	for _, row := range b.expressions {
		expressions = append(expressions, row...)
	}
	var from LogicalNode
	if b.update != nil {
		from = b.update.from
		expressions = append(expressions, b.update.assignments...)
		if b.update.predicate != nil {
			expressions = append(expressions, b.update.predicate)
		}
	}
	if b.deleteUsing != nil {
		from = b.deleteUsing
	}
	if b.predicate != nil {
		expressions = append(expressions, b.predicate)
	}
	if b.returning != nil {
		expressions = append(expressions, b.returning.expressions...)
	}
	if from == nil {
		from = &singletonNode{}
	}
	metadata.query = &BoundQuery{Root: &projectNode{child: from, expressions: expressions, syntax: query, frame: 1}}
	metadata = retainRuleCTEPresentation(auth.catalog, metadata)
	presentation, err := e.freezeQueryPresentationLocked(auth, query, metadata, false, false, 0, "", false)
	if err != nil {
		return nil, err
	}
	presentation.frames[1] = metadata.query.Root.(*projectNode)
	return presentation, nil
}

func (e *Engine) freezeRuleModifierPresentationsLocked(auth AuthorizationView, b binding) (map[parser.Statement]*viewDefinition, error) {
	var presentations map[parser.Statement]*viewDefinition
	var collect func(binding) error
	collect = func(body binding) error {
		for _, definition := range body.ctes {
			if definition.mutation == nil || presentations[definition.statement] != nil {
				continue
			}
			// Auxiliary bindings share the outer statement binder's capture
			// tables. Those tables are published on the completed action binding,
			// not on each mutation. Borrow them for freezing while retaining the
			// modifier's own executable query and globally indexed source leaves.
			metadata := *definition.mutation
			metadata.dependencies = b.dependencies
			metadata.viewLiterals, metadata.viewCastUsages = b.viewLiterals, b.viewCastUsages
			metadata.viewIntaggRefs = b.viewIntaggRefs
			metadata.viewOperatorRefs = b.viewOperatorRefs
			metadata.viewComposites = b.viewComposites
			metadata.viewSimilarSubstrings, metadata.viewPosixSubstrings = b.viewSimilarSubstrings, b.viewPosixSubstrings
			metadata.viewCollations = b.viewCollations
			metadata.viewColumns, metadata.viewStars = b.viewColumns, b.viewStars
			metadata.viewNamespaces = b.viewNamespaces
			metadata.sequenceConstants, metadata.regtypeConstants = b.sequenceConstants, b.regtypeConstants
			presentation, err := e.freezeRuleActionPresentationLocked(auth, definition.statement, metadata)
			if err != nil {
				return err
			}
			if presentations == nil {
				presentations = make(map[parser.Statement]*viewDefinition)
			}
			presentations[definition.statement] = presentation
			if err := collect(*definition.mutation); err != nil {
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
