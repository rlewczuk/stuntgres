package engine

import "stuntgres/internal/parser"

func (e *Engine) bindInsertReturning(b *binding, binder *expressionBinder, auth AuthorizationView, s *parser.Insert) error {
	if s.Returning == nil {
		return nil
	}
	binder.target.relation, binder.target.ref = b.relation, b.ref
	binder.target.name, binder.target.alias = b.relation.Name(), s.Alias.Text
	binder.query.auth = &auth
	target := binder.mutationTargetNamespace()
	binder.query.namespace = &target
	binder.query.hidden = nil
	if s.Alias.Text != "" {
		binder.query.hidden = append(binder.query.hidden, target.qualifiers[0])
	}
	if s.Conflict != nil && s.Conflict.Update {
		binder.query.hidden = append(binder.query.hidden, queryQualifier{name: "excluded", original: "excluded", lateralForbidden: true})
	}
	previousUses := binder.target.uses
	previousReadRequired := binder.target.returningReadRequired
	binder.target.returningReadRequired = false
	binder.target.uses = nil
	output, err := binder.bindReturning(s.Returning, returningInsert)
	reads := binder.target.uses
	readRequired := binder.target.returningReadRequired
	binder.target.returningReadRequired = previousReadRequired
	binder.target.uses = previousUses
	for _, slot := range reads {
		found := false
		for _, prior := range binder.target.uses {
			if prior == slot {
				found = true
				break
			}
		}
		if !found {
			binder.target.uses = append(binder.target.uses, slot)
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
