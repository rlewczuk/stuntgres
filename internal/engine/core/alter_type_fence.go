package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
)

// A semantic TYPE action protects stored codecs even when its declared type is
// unchanged. Retirement-only dependency observations cannot fence this graph:
// the nominal row type intentionally remains present in the candidate.
type AlterTypeUseFence struct {
	Source  *catalog.Relation
	Targets []*catalog.Relation
}

func (AlterTypeUseFence) Kind() session.FenceKind { return fenceAlterTypeUse }

func (f AlterTypeUseFence) CurrentLocked(e *Engine, changes *session.Journal) error {
	view := e.PrivateCatalogLocked(changes)
	if err := typedef.RowRewriteError(view, f.Source); err != nil {
		return catalog.Conflict{}
	}
	for _, target := range f.Targets {
		if err := typedef.RowRewriteError(view, target); err != nil {
			return catalog.Conflict{}
		}
	}
	return nil
}
