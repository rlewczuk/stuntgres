package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
)

// stagePartitionIndexValidity runs the validity pass over the stage. It
// needs no lock.
func (e *Engine) stagePartitionIndexValidity(stage *CatalogStage, promote ids.ObjectAddress) error {
	return index.PartitionIndexValidity(stageDropProvider(context.Background(), stage, nil, nil, nil), promote)
}

func (s *StatementScope) partitionIndexTransaction(stmt parser.Statement) bool {
	if v, ok := stmt.(*parser.AlterIndex); ok {
		return v.Action == "attach"
	}
	drop, ok := stmt.(*parser.DropIndex)
	if !ok {
		return false
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(s.op, stmt)
	if err != nil {
		return false
	}
	if len(drop.Names) == 0 {
		return false
	}
	for _, name := range drop.Names {
		ref, err := e.resolveRelationReference(auth, name, true)
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
