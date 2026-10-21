package extension

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// DropTargets selects the installed extensions DROP EXTENSION
// removes, in statement order without duplicates. A missing name fails, or
// with IF EXISTS emits a notice and is skipped; every selected extension must
// be owned by the current user.
func DropTargets(auth ddl.Authority, view access.View, s *parser.DropExtension, notice func(sqlstate.Error)) ([]ids.ObjectAddress, error) {
	var selected []ids.ObjectAddress
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name.Text})
		if address == (ids.ObjectAddress{}) {
			if !s.IfExists {
				return nil, sqlError("42704", fmt.Sprintf(`extension "%s" does not exist`, name.Text))
			}
			notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`extension "%s" does not exist, skipping`, name.Text)})
			continue
		}
		if seen[address] {
			continue
		}
		if err := view.Check(auth, access.Requirement{Object: view.Owner.Domain.Ref(view.Catalog.Epoch(), address), OwnerOnly: true, OwnerNoun: "extension"}); err != nil {
			return nil, err
		}
		selected = append(selected, address)
		seen[address] = true
	}
	return selected, nil
}
