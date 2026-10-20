package typedef

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func StageDropCompositeLocked(h StageHost, auth Authority, s *parser.DropComposite) error {
	view := h.AccessView(auth)
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		typ, err := h.NamedType(auth, name)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42704" {
				if s.IfExists {
					Notice(h, "00000", fmt.Sprintf(`type "%s" does not exist, skipping`, catalog.ObjectNameText(name)))
					continue
				}
				return sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(name)))
			}
			return err
		}
		if _, err := StandaloneComposite(view.Catalog, typ, nil); err != nil {
			return expr.ErrorAt("0A000", "statement is unsupported", s.Pos)
		}
		ref := ObjectRef(h, auth, typ.Address())
		if err := Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if !seen[ref.Address] {
			seen[ref.Address] = true
			h.DropTarget(ref.Address)
		}
	}
	h.RecordDependencyPath(auth)
	return nil
}
