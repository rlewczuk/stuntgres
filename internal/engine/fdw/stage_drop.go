package fdw

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// stageDrop stages DROP of the wrappers, servers or the user mapping s
// names and returns the dropped roots. IF EXISTS skips a missing object with
// a NOTICE.
func stageDrop(h Host, auth ddl.Authority, s *parser.ForeignDDL, target stageTarget) ([]ids.ObjectAddress, error) {
	view := h.AccessView(auth)
	class := target.class
	names := s.Names
	if s.Kind == "mapping" {
		names = []parser.ObjectName{{Parts: []parser.Identifier{{Text: target.name}}}}
	}
	var roots []ids.ObjectAddress
	for _, n := range names {
		if len(n.Parts) != 1 {
			return nil, sqlError("42601", "SQL/MED global names cannot be qualified")
		}
		d, er := Find(view.Catalog, class, n.Parts[0].Text)
		if er != nil {
			if class == ids.ForeignMappingClassID {
				message := MappingDiagnostic(view, target.role, target.server, false)
				if s.IfExists {
					ddl.Notice(h, "00000", message+", skipping")
					continue
				}
				return nil, sqlError("42704", message)
			}
			if s.IfExists {
				ddl.Notice(h, "00000", fmt.Sprintf(`%s "%s" does not exist, skipping`, Label(class), n.Parts[0].Text))
				continue
			}
			return nil, er
		}
		if class != ids.ForeignMappingClassID {
			if er = ddl.Check(h, auth, access.Requirement{Object: ObjectRef(view, d), OwnerOnly: true}); er != nil {
				return nil, er
			}
		}
		if er = h.RemoveObject(ObjectRef(view, d), false); er != nil {
			return nil, er
		}
		roots = append(roots, d.Address())
	}
	return roots, nil
}
