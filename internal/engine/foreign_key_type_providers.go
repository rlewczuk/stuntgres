package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
)

func acquireForeignKeyTypeProviders(s *core.StatementScope, view catalog.Read, address ids.ObjectAddress) error {
	for _, ref := range table.ForeignKeyTypeProviders(view, s.Engine().Catalog.Domain, address) {
		if err := s.ForeignKeyGrant(ref, 0, lock.RowShare, nil); err != nil {
			return err
		}
	}
	return nil
}
