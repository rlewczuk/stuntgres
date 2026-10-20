package analyze

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Transform FROM, WHERE, RETURNING (output fallback), then RHS/destinations.
func bindUpdate(b *Binding, binder *Binder, s *parser.Update) error {
	if s.CurrentOf != nil {
		b.CurrentOf = s.CurrentOf.Text
	}
	binder.Target.Relation, binder.Target.Ref = b.Relation, b.Ref
	binder.Target.Name = b.Relation.Name()
	binder.Target.alias = s.Alias.Text
	binder.Params.UpdateParameters = true
	defer func() {
		binder.Params.UpdateParameters = false
		binder.Params.updateInputParameters = nil
	}()
	u := &UpdateBinding{}
	var err error
	target := binder.mutationTargetNamespace()
	binder.Query.Hidden = append(binder.Query.Hidden, target.Qualifiers[0])
	if s.From != nil {
		var sources QueryNamespace
		u.From, sources, err = binder.BindFrom(s.From)
		if err != nil {
			return err
		}
		u.SourceWidth = len(sources.Frame)
		target, err = combineQueryNamespace(target, rebaseQueryNamespace(sources, len(b.Relation.Columns)))
		if err != nil {
			return err
		}
	}
	binder.Query.Namespace = &target
	binder.Clause.AggregateClause = "UPDATE"
	if s.Where != nil {
		u.Predicate, err = binder.Bind(s.Where, ExpressionPolicy{Context: FilterContext})
		if err != nil {
			return err
		}
	}
	if s.Returning != nil {
		b.Returning, err = binder.BindReturning(s.Returning, returningUpdate)
		if err != nil {
			return err
		}
	}
	if s.Returning != nil {
		// Only WHERE and finalized RETURNING types precede SET inference.
		// SET's own unresolved occurrences retain their contextual diagnostics.
		for number, usage := range binder.Params.Parameters {
			if usage.Type != nil {
				if binder.Params.updateInputParameters == nil {
					binder.Params.updateInputParameters = make(map[int]sqltype.Usage, len(binder.Params.Parameters))
				}
				binder.Params.updateInputParameters[number] = usage
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
