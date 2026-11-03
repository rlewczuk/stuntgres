package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

// Consumes Engine.Mu. The cluster mutex precedes only the provider's leaf lock;
// manager metadata is entered after it is released. No peer admission is taken.
func (e *Engine) capturePGStatActivityLocked(scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation, keep pgcatalog.Keep) (*exec.Source, error) {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	reader := statsReader(scope.Op.Session, scope.Op.EffectiveSettings())
	provider := e.activityProvider()
	live := provider.CaptureActivities()
	if !reader.ActivityCaptured() {
		for token, a := range live {
			target := e
			if e.Cluster != nil {
				target = e.Cluster.Targets[a.DatabaseName]
			}
			if target != nil {
				tx := a.Command.Transaction
				if tx.Session() == token && tx.Domain() == target.Catalog.Domain && tx.Epoch() == target.Catalog.State.Epoch() {
					entry, exists := target.Visibility.Entries[tx]
					if exists {
						a.XID = target.Visibility.AssignedXIDLocked(tx)
						xmin := entry.ActivityFullXmin
						for _, h := range []uint64{entry.ImportedFullXmin, entry.ExportFullXmin} {
							if h != 0 && (xmin == 0 || h < xmin) {
								xmin = h
							}
						}
						a.Xmin = uint32(xmin)
					}
				}
			}
			live[token] = a
		}
		reader.InstallActivities(live)
	}
	activities := reader.Activities(provider)
	in := pgcatalog.Input{Context: scope.Op.Context(), Owner: &e.Catalog.Owner, Ref: ref, Source: source, Keep: keep, Catalog: auth.Catalog, Roles: auth.Roles, ActivityObserver: auth.State.CurrentUser, Activities: activities}
	e.Mu.Unlock()
	waits := e.Locks.ActivityWaits()
	for token, a := range activities {
		a.WaitType, a.Wait = "", ""
		current, exists := live[token]
		if exists && current.Active {
			a.WaitType, a.Wait = current.WaitType, current.Wait
			if wait, ok := waits[token]; ok && wait.Transaction == current.Command.Transaction {
				a.WaitType, a.Wait = "Lock", wait.Event
			}
		}
		activities[token] = a
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.StatActivityOID)
	return catalogrt.CaptureRows(in, produce)
}
