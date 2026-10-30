package core

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

type VacuumProgress struct {
	Fact  pgcatalog.VacuumProgressFact
	Owner authorization.PrincipalRef
}

func (e *Engine) UpdateDatabaseMaintenanceLocked() {
	// A still-active publisher can create or first write a relation after this
	// calculation; installed relation bounds alone cannot justify the seed.
	cutoff, err := e.Visibility.VacuumCutoffLocked()
	if err != nil {
		return
	}
	next, ok := table.DatabaseFrozenXIDLocked(e.Catalog.State, e.Storage.Relations, cutoff)
	if !ok {
		return
	}
	e.DatabaseFrozenXID = next
	e.publishDatabaseFrozenXIDLocked()
}
func (r EngineCatalogRuntime) Maintenance(address ids.ObjectAddress) (pgcatalog.MaintenanceFact, bool) {
	store := r.engine.Storage.Relations[address]
	if store == nil {
		store = r.engine.PrivateStorageLocked(r.Auth.PendingCatalog(), address)
	}
	var fact pgcatalog.MaintenanceFact
	if store != nil {
		m := store.Maintenance
		fact = pgcatalog.MaintenanceFact{FrozenXID: m.FrozenXID, Scanned: m.Scanned, Live: m.Live, Dead: m.Dead, Tuples: m.Tuples, Inserts: m.Inserts, Updates: m.Updates, Deletes: m.Deletes, Modifications: m.Modifications, InsertsSince: m.InsertsSince, VacuumCount: m.VacuumCount, LastVacuum: m.LastVacuum}
	}
	metadata := session.ResolveRelationMetadata(r.Auth.PendingCatalog(), r.engine.Storage.RelationMetadata, address)
	if metadata != nil {
		fact.AnalyzeCount, fact.LastAnalyze = metadata.AnalyzeCount, metadata.LastAnalyze
		fact.InheritedScanned, fact.InheritedTuples = metadata.InheritedScanned, metadata.InheritedTuples
		if foreign := metadata.ForeignAnalyze; foreign != nil {
			fact.ForeignAnalyzed = true
			fact.ForeignRows, fact.ForeignDeadRows, fact.ForeignPages = foreign.TotalRows, foreign.TotalDeadRows, foreign.TotalPages
		}
		if store == nil {
			fact.Scanned, fact.Tuples = metadata.Scanned, metadata.Tuples
		}
	}
	imported := session.ResolveRelationStatistics(r.Auth.PendingCatalog(), address, metadata)
	if imported != nil { fact.Imported = *imported }
	return fact, store != nil || metadata != nil || imported != nil
}
func (r EngineCatalogRuntime) DatabaseFrozenXID() uint64 { return r.engine.DatabaseFrozenXID }
func (r EngineCatalogRuntime) VisitVacuumProgress(ctx context.Context, visit func(pgcatalog.VacuumProgressFact) error) error {
	if len(r.engine.VacuumProgress) == 0 {
		return nil
	}
	viewer, err := pgcatalog.NewProgressViewer(ctx, r.Auth.Roles, r.Auth.State.CurrentUser)
	if err != nil {
		return err
	}
	for _, p := range r.engine.VacuumProgress {
		fact := p.Fact
		fact.Visible = viewer.Visible(p.Owner)
		if err := visit(fact); err != nil {
			return err
		}
	}
	return nil
}
