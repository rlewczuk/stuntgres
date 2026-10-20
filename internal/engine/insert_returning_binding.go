package engine

import "stuntgres/internal/parser"

func (e *Engine) bindInsertReturning(b *binding, binder *expressionBinder, auth AuthorizationView, s *parser.Insert) error {
	if s.Returning == nil {
		return nil
	}
	binder.relation, binder.ref = b.relation, b.ref
	binder.targetName, binder.targetAlias = b.relation.Name(), s.Alias.Text
	binder.queryAuth = &auth
	target := binder.mutationTargetNamespace()
	binder.queryNamespace = &target
	binder.queryHidden = nil
	if s.Alias.Text != "" {
		binder.queryHidden = append(binder.queryHidden, target.qualifiers[0])
	}
	if s.Conflict != nil && s.Conflict.Update {
		binder.queryHidden = append(binder.queryHidden, queryQualifier{name: "excluded", original: "excluded", lateralForbidden: true})
	}
	previousUses := binder.uses
	previousReadRequired := binder.returningReadRequired
	binder.returningReadRequired = false
	binder.uses = nil
	output, err := binder.bindReturning(s.Returning, returningInsert)
	reads := binder.uses
	readRequired := binder.returningReadRequired
	binder.returningReadRequired = previousReadRequired
	binder.uses = previousUses
	for _, slot := range reads {
		found := false
		for _, prior := range binder.uses {
			if prior == slot {
				found = true
				break
			}
		}
		if !found {
			binder.uses = append(binder.uses, slot)
		}
	}
	if err != nil {
		return err
	}
	output.reads = reads
	output.readRequired = readRequired
	b.returning = output
	if b.insert.conflict != nil {
		b.insert.conflict.returning = output
	}
	return nil
}
