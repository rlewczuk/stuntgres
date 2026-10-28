package table

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func TriggerModeAction(action parser.AlterTableActionKind) bool {
	return action >= parser.EnableTrigger && action <= parser.EnableAlwaysTrigger
}

func TriggerModesOnly(actions []parser.AlterTableAction) bool {
	return len(actions) != 0 && !slices.ContainsFunc(actions, func(action parser.AlterTableAction) bool {
		return !TriggerModeAction(action.Action)
	})
}

func TriggerActionMode(action parser.AlterTableActionKind) catalog.TriggerMode {
	switch action {
	case parser.EnableTrigger:
		return catalog.TriggerOrigin
	case parser.DisableTrigger:
		return catalog.TriggerDisabled
	case parser.EnableReplicaTrigger:
		return catalog.TriggerReplica
	case parser.EnableAlwaysTrigger:
		return catalog.TriggerAlways
	}
	return 0
}

// TriggerModeCandidate borrows the current statement catalog. All replacements
// are private envelopes; definitions and executable backing are never mutated.
func TriggerModeCandidate(ctx context.Context, read catalog.Read, target *catalog.Relation, action parser.AlterTableAction, recurse, superuser bool) ([]*catalog.Relation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mode := TriggerActionMode(action.Action)
	if !mode.Valid() {
		return nil, fmt.Errorf("stuntgres: invalid trigger mode action")
	}
	// Each owner gets one envelope, with independently lazy payload copies.
	// Subsequent selected entries update only invocation-private backing.
	type ownerEdit struct {
		relation catalog.Relation
		users, keys bool
	}
	changed := make(map[ids.ObjectAddress]*ownerEdit)
	private := func(owner *catalog.Relation) *ownerEdit {
		if edit := changed[owner.Address()]; edit != nil {
			return edit
		}
		edit := &ownerEdit{relation: *owner}
		changed[owner.Address()] = edit
		return edit
	}
	replaceUser := func(owner *catalog.Relation, index int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		list := stored.TriggersOf(owner)
		trigger := list[index]
		if trigger.Mode() == mode {
			return nil
		}
		replacement, err := trigger.WithMode(mode)
		if err != nil {
			return err
		}
		edit := private(owner)
		if !edit.users {
			edit.relation.Triggers = stored.TriggersPayload(slices.Clone(list))
			edit.users = true
		}
		stored.TriggersOf(&edit.relation)[index] = replacement
		return nil
	}
	var visitClones func(*catalog.Relation, map[ids.OID]struct{}) error
	visitClones = func(parentRelation *catalog.Relation, parents map[ids.OID]struct{}) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, address := range parentRelation.Partition.Children {
			owner, ok := read.Relation(address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			var descendants map[ids.OID]struct{}
			for i, trigger := range stored.TriggersOf(owner) {
				if err := ctx.Err(); err != nil {
					return err
				}
				parent := trigger.Parts().Parent
				if parent.ClassID != 2620 {
					continue
				}
				if _, selected := parents[parent.ObjectID]; !selected {
					continue
				}
				if err := replaceUser(owner, i); err != nil {
					return err
				}
				if owner.Partition != nil && len(owner.Partition.Children) != 0 {
					if descendants == nil {
						descendants = make(map[ids.OID]struct{})
					}
					descendants[trigger.OID()] = struct{}{}
				}
			}
			if len(descendants) != 0 {
				if err := visitClones(owner, descendants); err != nil {
					return err
				}
			}
		}
		return nil
	}
	found := false
	var parents map[ids.OID]struct{}
	for i, trigger := range stored.TriggersOf(target) {
		if action.TriggerSelector == parser.TriggerNamed && trigger.Name() != action.Trigger.Text {
			continue
		}
		found = true
		if err := replaceUser(target, i); err != nil {
			return nil, err
		}
		if recurse && target.Partition != nil && len(target.Partition.Children) != 0 && trigger.Parts().Level == stored.TriggerRow {
			if parents == nil {
				parents = make(map[ids.OID]struct{})
			}
			parents[trigger.OID()] = struct{}{}
		}
	}
	if len(parents) != 0 {
		if err := visitClones(target, parents); err != nil {
			return nil, err
		}
	}
	if action.TriggerSelector != parser.TriggerUser {
		// Facts are grouped by owner and constraint. Advance through each
		// borrowed FK slice once, rather than searching it for every slot.
		var owner *catalog.Relation
		index := 0
		for fact := range catalog.ForeignKeyTriggerFacts(read) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if fact.Relation != target.Address() || action.TriggerSelector == parser.TriggerNamed && fact.Name != action.Trigger.Text {
				continue
			}
			found = true
			if !superuser {
				return nil, sqlError("42501", fmt.Sprintf(`permission denied: "%s" is a system trigger`, fact.Name))
			}
			if owner == nil || owner.Address() != fact.Owner {
				var ok bool
				owner, ok = read.Relation(fact.Owner)
				if !ok {
					return nil, ids.ErrCatalogReplaced
				}
				index = 0
			}
			keys := owner.Constraints.ForeignKeys
			for index < len(keys) && keys[index].OID != fact.Constraint.OID {
				index++
			}
			if index == len(keys) {
				return nil, ids.ErrCatalogReplaced
			}
			if keys[index].TriggerMode(fact.Slot) == mode {
				continue
			}
			edit := private(owner)
			if !edit.keys {
				edit.relation.Constraints.ForeignKeys = slices.Clone(keys)
				edit.keys = true
			}
			edit.relation.Constraints.ForeignKeys[index].TriggerModes[fact.Slot] = mode
		}
	}
	if !found && action.TriggerSelector == parser.TriggerNamed {
		return nil, sqlError("42704", fmt.Sprintf(`trigger "%s" for table "%s" does not exist`, action.Trigger.Text, target.Name()))
	}
	result := make([]*catalog.Relation, 0, len(changed))
	for _, next := range changed {
		result = append(result, &next.relation)
	}
	slices.SortFunc(result, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	return result, nil
}

func RestoreTriggerMode(statement *parser.AlterTable) bool {
	return !statement.IfExists && statement.Only && len(statement.Name.Parts) == 2 && len(statement.Actions) == 1 && TriggerModeAction(statement.Actions[0].Action) && statement.Actions[0].TriggerSelector == parser.TriggerNamed
}
