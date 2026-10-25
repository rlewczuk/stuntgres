package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (s *StatementScope) partitionIndexTransaction(stmt parser.Statement) bool {
	if v, ok := stmt.(*parser.AlterIndex); ok {
		return v.Action == "attach"
	}
	drop, ok := stmt.(*parser.DropIndex)
	if !ok || drop.Concurrent.Character != 0 {
		return false
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.StatementAuthorizationLocked(s.Op, stmt)
	if err != nil {
		return false
	}
	if len(drop.Names) == 0 {
		return false
	}
	for _, name := range drop.Names {
		ref, err := e.ResolveRelationReference(auth, name, true)
		if err != nil {
			return false
		}
		index, ok := auth.Catalog.Relation(ref.Address)
		if !ok || index.SQLIndex == nil {
			return false
		}
		table, ok := auth.Catalog.Relation(index.SQLIndex.Parent)
		if !ok || table.Partition == nil && index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			return false
		}
	}
	return true
}
