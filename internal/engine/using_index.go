package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

// prepareUsingIndexMetadata prepares the primary key's NOT NULL constraints
// (table code), then the attachment under Engine.mu; the caller holds
// nothing.
func (e *Engine) prepareUsingIndexMetadata(op *session.Operation, view catalog.Read, reader catalog.DefinitionReader, parent *catalog.Relation, captured index.UsingIndexAttachment) (*index.UsingIndexPreparation, error) {
	effective := parent
	if !captured.NotNullPrepared {
		candidate, err := table.UsingIndexNotNullCandidate(op, view, reader, parent, captured)
		if err != nil {
			return nil, err
		}
		effective = candidate
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return index.PrepareUsingIndex(DDLHost{E: e, Op: op}, reader, parent, effective, captured)
}

// Runtime effects follow action order, but remain immutable statement candidates.
// Row nullability is validated by the existing final effective-row ALTER scan.
// The caller holds nothing.
func (e *Engine) stageUsingIndex(op *session.Operation, stage *CatalogStage, parent *catalog.Relation, captured index.UsingIndexAttachment) (*catalog.Relation, error) {
	prepared, err := e.prepareUsingIndexMetadata(op, e.StageRead(stage), StageDefinitions(stage), parent, captured)
	if err != nil {
		return nil, err
	}
	return index.StageUsingIndex(DDLHost{E: e, Op: op, CatalogStage: stage}, parent, prepared)
}
