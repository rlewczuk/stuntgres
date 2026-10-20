package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Transform FROM, WHERE, RETURNING (output fallback), then RHS/destinations.
func bindUpdate(b *binding, binder *expressionBinder, s *parser.Update) error {
	if s.CurrentOf != nil {
		b.CurrentOf = s.CurrentOf.Text
	}
	binder.target.relation, binder.target.ref = b.Relation, b.Ref
	binder.target.name = b.Relation.Name()
	binder.target.alias = s.Alias.Text
	binder.params.updateParameters = true
	defer func() {
		binder.params.updateParameters = false
		binder.params.updateInputParameters = nil
	}()
	u := &boundUpdate{}
	var err error
	target := binder.mutationTargetNamespace()
	binder.query.hidden = append(binder.query.hidden, target.Qualifiers[0])
	if s.From != nil {
		var sources queryNamespace
		u.From, sources, err = binder.bindFrom(s.From)
		if err != nil {
			return err
		}
		u.SourceWidth = len(sources.Frame)
		target, err = combineQueryNamespace(target, rebaseQueryNamespace(sources, len(b.Relation.Columns)))
		if err != nil {
			return err
		}
	}
	binder.query.namespace = &target
	binder.clause.aggregateClause = "UPDATE"
	if s.Where != nil {
		u.Predicate, err = binder.bind(s.Where, expressionPolicy{context: filterContext})
		if err != nil {
			return err
		}
	}
	if s.Returning != nil {
		b.Returning, err = binder.bindReturning(s.Returning, returningUpdate)
		if err != nil {
			return err
		}
	}
	if s.Returning != nil {
		// Only WHERE and finalized RETURNING types precede SET inference.
		// SET's own unresolved occurrences retain their contextual diagnostics.
		for number, usage := range binder.params.parameters {
			if usage.Type != nil {
				if binder.params.updateInputParameters == nil {
					binder.params.updateInputParameters = make(map[int]sqltype.Usage, len(binder.params.parameters))
				}
				binder.params.updateInputParameters[number] = usage
			}
		}
	}
	if err := bindUpdateAssignments(b, binder, s.Assignments, u); err != nil {
		return err
	}
	if s.CurrentOf != nil && b.Relation.View != nil {
		return sqlError("0A000", "WHERE CURRENT OF on a view is not implemented")
	}
	return nil
}
