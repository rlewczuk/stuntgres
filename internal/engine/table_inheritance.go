package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func inheritanceHasChildren(v catalog.Read, parent ids.ObjectAddress) bool {
	for _, descriptor := range v.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok && d.Inheritance != nil && slices.Contains(d.Inheritance.Parents, parent) {
			return true
		}
	}
	return false
}

func inheritanceChildren(v catalog.Read, parent ids.ObjectAddress) []*catalog.Relation {
	var children []*catalog.Relation
	for address := range v.Descriptors() {
		if address.ClassID != ids.RelationClassID {
			continue
		}
		d, ok := v.Relation(address)
		if ok && d.Inheritance != nil && slices.Contains(d.Inheritance.Parents, parent) {
			children = append(children, d)
		}
	}
	slices.SortFunc(children, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	return children
}
func inheritanceParticipant(v catalog.Read, d *catalog.Relation) bool {
	return d.Inheritance != nil || inheritanceHasChildren(v, d.Address())
}

func inheritanceTypeName(u sqltype.Usage) string {
	name := u.Type.DiagnosticName()
	if u.Typmod != u.Type.Usage().Typmod {
		declaration, _ := u.Declaration()
		name += strings.TrimPrefix(declaration, u.Type.SQLName())
	}
	return name
}
func inheritanceColumnConflict(view catalog.Read, old, next catalog.Column, local bool) error {
	prefix := "inherited column"
	if local {
		prefix = "column"
	}
	if old.Usage.Type != next.Usage.Type || old.Usage.Typmod != next.Usage.Typmod {
		d := sqlError("42804", fmt.Sprintf(`%s "%s" has a type conflict`, prefix, old.Name)).(*sqlstate.Error)
		d.Detail = inheritanceTypeName(old.Usage) + " versus " + inheritanceTypeName(next.Usage)
		return d
	}
	if old.Usage.Collation.ID != next.Usage.Collation.ID {
		d := sqlError("42P21", fmt.Sprintf(`%s "%s" has a collation conflict`, prefix, old.Name)).(*sqlstate.Error)
		d.Detail = fmt.Sprintf(`"%s" versus "%s"`, typedef.CollationName(view, old.Usage.Collation.ID), typedef.CollationName(view, next.Usage.Collation.ID))
		return d
	}
	return nil
}
