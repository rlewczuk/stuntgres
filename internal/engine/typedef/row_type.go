package typedef

import (
	"fmt"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func ChooseAssociatedArrayName(name string, occupied func(string) bool) string {
	prefix := name
	for len(prefix) > 62 {
		_, width := utf8.DecodeLastRuneInString(prefix)
		prefix = prefix[:len(prefix)-width]
	}
	arrayName := "_" + prefix
	for suffix := 1; occupied(arrayName); suffix++ {
		tail := fmt.Sprintf("_%d", suffix)
		prefix := name
		for len(prefix) > 62-len(tail) {
			_, width := utf8.DecodeLastRuneInString(prefix)
			prefix = prefix[:len(prefix)-width]
		}
		arrayName = "_" + prefix + tail
	}
	return arrayName
}

// The executable projection shares one authority with named and supported user
// rows; catalog/builtin row identities remain lookup-only.
func CompositeMembers(relation *catalog.Relation) []sqltype.CompositeMember {
	members := make([]sqltype.CompositeMember, len(relation.Columns))
	for i, c := range relation.Columns {
		members[i] = sqltype.CompositeMember{Attribute: relation.Attributes[i], Name: c.Name, Usage: c.Usage}
	}
	return members
}
