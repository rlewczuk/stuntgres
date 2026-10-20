package analyze

import (
	"stuntgres/internal/parser"
)

func bindInsertReturning(b *Binding, binder *Binder, auth Authority, s *parser.Insert) error {
	if s.Returning == nil {
		return nil
	}
	binder.Target.Relation, binder.Target.Ref = b.Relation, b.Ref
	binder.Target.Name, binder.Target.alias = b.Relation.Name(), s.Alias.Text
	binder.Query.Auth = auth
	target := binder.mutationTargetNamespace()
	binder.Query.Namespace = &target
	binder.Query.Hidden = nil
	if s.Alias.Text != "" {
		binder.Query.Hidden = append(binder.Query.Hidden, target.Qualifiers[0])
	}
	if s.Conflict != nil && s.Conflict.Update {
		binder.Query.Hidden = append(binder.Query.Hidden, QueryQualifier{Name: "excluded", Original: "excluded", LateralForbidden: true})
	}
	previousUses := binder.Target.Uses
	previousReadRequired := binder.Target.returningReadRequired
	binder.Target.returningReadRequired = false
	binder.Target.Uses = nil
	output, err := binder.BindReturning(s.Returning, returningInsert)
	reads := binder.Target.Uses
	readRequired := binder.Target.returningReadRequired
	binder.Target.returningReadRequired = previousReadRequired
	binder.Target.Uses = previousUses
	for _, slot := range reads {
		found := false
		for _, prior := range binder.Target.Uses {
			if prior == slot {
				found = true
				break
			}
		}
		if !found {
			binder.Target.Uses = append(binder.Target.Uses, slot)
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
