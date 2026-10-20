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
	if b.delete.using != nil {
		from = b.delete.using
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
	metadata = retainRuleCTEPresentation(auth.Catalog, metadata)
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
			mutation, ok := mutationBinding(definition)
			if !ok || presentations[definition.statement] != nil {
				continue
			}
			// Auxiliary bindings share the outer statement binder's capture
			// tables. Those tables are published on the completed action binding,
			// not on each mutation. Borrow them for freezing while retaining the
			// modifier's own executable query and globally indexed source leaves.
			metadata := *mutation
			metadata.dependencies = b.dependencies
			metadata.capture.literals, metadata.capture.castUsages = b.capture.literals, b.capture.castUsages
			metadata.capture.routineRefs = b.capture.routineRefs
			metadata.capture.operatorRefs = b.capture.operatorRefs
			metadata.capture.composites = b.capture.composites
			metadata.capture.similarSubstrings, metadata.capture.posixSubstrings = b.capture.similarSubstrings, b.capture.posixSubstrings
			metadata.capture.collations = b.capture.collations
			metadata.capture.columns, metadata.capture.stars = b.capture.columns, b.capture.stars
			metadata.capture.namespaces = b.capture.namespaces
			metadata.prepared.sequenceConstants, metadata.prepared.regtypeConstants = b.prepared.sequenceConstants, b.prepared.regtypeConstants
			presentation, err := e.freezeRuleActionPresentationLocked(auth, definition.statement, metadata)
			if err != nil {
				return err
			}
			if presentations == nil {
				presentations = make(map[parser.Statement]*viewDefinition)
			}
			presentations[definition.statement] = presentation
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
