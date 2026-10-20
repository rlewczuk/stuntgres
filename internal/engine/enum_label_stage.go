package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// enumLabelEdit is the outcome of staging ALTER TYPE ... ADD or RENAME VALUE.
type enumLabelEdit struct {
	Skipped bool  // IF NOT EXISTS met an existing label; nothing was staged
	Added   bool  // a member was added
	Member  int64 // the added member's identity
}

// stageAlterEnumLabelLocked stages old with its labels edited. The caller owns
// the enum journal.
func stageAlterEnumLabelLocked(h typedef.StageHost, old *catalog.Type, alter *parser.AlterEnum) (enumLabelEdit, error) {
	members := old.SQLType.EnumMembers()
	oldIndex, newIndex := -1, -1
	for i, m := range members {
		if m.Label == alter.Old.Text {
			oldIndex = i
		}
		if m.Label == alter.Label.Text {
			newIndex = i
		}
	}
	if alter.Rename && oldIndex < 0 {
		return enumLabelEdit{}, sqlError("22023", fmt.Sprintf(`"%s" is not an existing enum label`, alter.Old.Text))
	}
	if newIndex >= 0 {
		if !alter.Rename && alter.IfNotExists {
			typedef.Notice(h, "42710", fmt.Sprintf(`enum label "%s" already exists, skipping`, alter.Label.Text))
			return enumLabelEdit{Skipped: true}, nil
		}
		return enumLabelEdit{}, sqlError("42710", fmt.Sprintf(`enum label "%s" already exists`, alter.Label.Text))
	}
	var edit enumLabelEdit
	if alter.Rename {
		members[oldIndex].Label = alter.Label.Text
	} else {
		at := len(members)
		if alter.Neighbor.Pos.Character != 0 {
			at = -1
			for i, m := range members {
				if m.Label == alter.Neighbor.Text {
					at = i
					break
				}
			}
			if at < 0 {
				return enumLabelEdit{}, sqlError("22023", fmt.Sprintf(`"%s" is not an existing enum label`, alter.Neighbor.Text))
			}
			if !alter.Before {
				at++
			}
		}
		member, err := h.AllocateEnumMember()
		if err != nil {
			return enumLabelEdit{}, err
		}
		order := typedef.EnumInsertionOrder(members, at)
		members = slices.Insert(members, at, sqltype.EnumMember{ID: member, Label: alter.Label.Text, SortOrder: order})
		edit = enumLabelEdit{Added: true, Member: member}
	}
	typ, err := sqltype.EnumWithMembers(old.SQLType.ID(), old.Name(), old.SQLType.SQLName(), members)
	if err != nil {
		return enumLabelEdit{}, err
	}
	revision, err := h.Revision()
	if err != nil {
		return enumLabelEdit{}, err
	}
	next := *old
	next.SQLType = typ
	next.ObjectRevision = revision
	if err := h.Stage().Replace(&next); err != nil {
		return enumLabelEdit{}, err
	}
	return edit, nil
}
