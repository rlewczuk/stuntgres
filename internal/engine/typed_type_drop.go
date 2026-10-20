package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) stageDropCompositeLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.DropComposite) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		typ, err := e.namedType(auth, name)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42704" {
				if s.IfExists {
					schemaNotice(op, "00000", fmt.Sprintf(`type "%s" does not exist, skipping`, objectNameText(name)))
					continue
				}
				return sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, objectNameText(name)))
			}
			return err
		}
		if _, err := StandaloneComposite(auth.Catalog, typ, nil); err != nil {
			return expr.ErrorAt("0A000", "statement is unsupported", s.Pos)
		}
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), typ.Address())
		if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if !seen[ref.Address] {
			seen[ref.Address] = true
			stage.domainTargets = append(stage.domainTargets, ref.Address)
		}
	}
	stage.dependencyPath, _ = e.referencePath(auth)
	return nil
}
