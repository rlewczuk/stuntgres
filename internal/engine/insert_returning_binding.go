package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
)

func bindInsertReturning(b *binding, binder *expressionBinder, auth analyze.Authority, s *parser.Insert) error {
	if s.Returning == nil {
		return nil
	}
	binder.target.relation, binder.target.ref = b.Relation, b.Ref
	binder.target.name, binder.target.alias = b.Relation.Name(), s.Alias.Text
	binder.query.auth = auth
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
	output.Reads = reads
	output.ReadRequired = readRequired
	b.Returning = output
	if b.Insert.Conflict != nil {
		b.Insert.Conflict.Returning = output
	}
	return nil
}
