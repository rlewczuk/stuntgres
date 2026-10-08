package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Stable attributes own defaults, ACLs, comments, constraints and dependency edges.
func (scope *StatementScope) dropColumnCandidate(stage *CatalogStage, old *relationDescriptor, ref ObjectRef, a parser.AlterTableAction) (*relationDescriptor, error) {
	if a.Mode != parser.DropRestrict && a.Mode != parser.DropCascade {
		return nil, fmt.Errorf("stuntgres: invalid column drop")
	}
	if isSystemColumn(a.Column.Text) {
		return nil, sqlError("0A000", fmt.Sprintf(`cannot drop system column "%s"`, a.Column.Text))
	}
	slot, exists := old.index[a.Column.Text]
	if !exists {
		if !a.IfExists {
			return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Column.Text, old.name))
		}
		scope.op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`column "%s" of relation "%s" does not exist, skipping`, a.Column.Text, old.name)})
		return old, nil
	}
	target := ref.Address
	target.SubID = old.attributes[slot]
	if err := stageViewDependents(stage, []ObjectAddress{target}, a.Mode); err != nil {
		return nil, err
	}
	if err := scope.stageOwnedSequences(stage, []ObjectAddress{target}, a.Mode); err != nil {
		return nil, err
	}
	next := *old
	next.columns = make([]ColumnDescriptor, 0, len(old.columns)-1)
	next.attributes = make([]int32, 0, len(old.attributes)-1)
	next.index = make(map[string]int, len(old.index)-1)
	for i, column := range old.columns {
		if err := scope.op.Context().Err(); err != nil {
			return nil, err
		}
		if i == slot {
			continue
		}
		next.index[column.Name] = len(next.columns)
		next.columns = append(next.columns, column)
		next.attributes = append(next.attributes, old.attributes[i])
	}
	next.notNullConstraints = nil
	for _, constraint := range old.notNullConstraints {
		if constraint.Attribute != old.attributes[slot] {
			next.notNullConstraints = append(next.notNullConstraints, constraint)
		}
	}
	if _, exists := old.defaults[old.attributes[slot]]; exists {
		next.defaults = nil
		if len(old.defaults) > 1 {
			next.defaults = make(map[int32]columnDefault, len(old.defaults)-1)
			for attribute, definition := range old.defaults {
				if attribute != old.attributes[slot] {
					next.defaults[attribute] = definition
				}
			}
		}
	}
	if _, exists := old.columnACL[old.attributes[slot]]; exists {
		next.columnACL = nil
		for sub, acl := range old.columnACL {
			if err := scope.op.Context().Err(); err != nil {
				return nil, err
			}
			if sub == old.attributes[slot] {
				continue
			}
			if next.columnACL == nil {
				next.columnACL = make(map[int32]ACLValue, len(old.columnACL)-1)
			}
			next.columnACL[sub] = acl
		}
	}
	if _, exists := old.columnComments[old.attributes[slot]]; exists {
		next.columnComments = nil
		for sub, text := range old.columnComments {
			if err := scope.op.Context().Err(); err != nil {
				return nil, err
			}
			if sub == old.attributes[slot] {
				continue
			}
			if next.columnComments == nil {
				next.columnComments = make(map[int32]string, len(old.columnComments)-1)
			}
			next.columnComments[sub] = text
		}
	}

	return &next, nil
}
