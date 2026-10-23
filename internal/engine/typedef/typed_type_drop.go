package typedef

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func StageDropTypeLocked(h StageHost, auth ddl.Authority, s *parser.DropType) error {
	return stageDropTypes(h, auth, s.Names, s.IfExists, false)
}

func stageDropTypes(h StageHost, auth ddl.Authority, names []parser.ObjectName, ifExists, domainOnly bool) error {
	view := h.AccessView(auth)
	path, _ := auth.ReferencePath()
	seen := make(map[ids.ObjectAddress]*catalog.Type, len(names))
	roots := make([]*catalog.Type, 0, len(names))
	for _, name := range names {
		typ, err := utilityType(h, auth, name)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ifExists && ok && (diagnostic.Code == "42704" || diagnostic.Code == "3F000") {
				ddl.Notice(h, "00000", diagnostic.Message+", skipping")
				continue
			}
			return err
		}
		if domainOnly && !typ.SQLType.IsDomain() {
			return sqlError("42809", fmt.Sprintf(`"%s" is not a domain`, catalog.ObjectNameText(name)))
		}
		ref := ddl.ObjectRef(h, auth, typ.Address())
		if err := ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); !ok || diagnostic.Code != "42501" {
				return err
			}
			if schemaErr := ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, typ.Namespace()), OwnerOnly: true}); schemaErr != nil {
				if diagnostic, ok := schemaErr.(*sqlstate.Error); !ok || diagnostic.Code != "42501" {
					return schemaErr
				}
				return err
			}
		}
		if seen[ref.Address] == nil {
			seen[ref.Address] = typ
			roots = append(roots, typ)
		}
	}
	// Resolve and authorize the entire explicit batch before dependency-root
	// protection or closure discovery can decide its removal.
	for _, typ := range roots {
		if err := catalog.TypeDropProtection(view.Catalog, typ, path, seen); err != nil {
			return err
		}
	}
	for _, typ := range roots {
		h.DropTarget(typ.Address())
	}
	_ = h.RecordDependencyPath(auth)
	return nil
}
