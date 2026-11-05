package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
)

// Consumes Engine.Mu. The cluster mutex precedes only the provider's leaf lock;
// manager metadata is entered after it is released. No peer admission is taken.
func (e *Engine) captureActivityViewsLocked(scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation, keep pgcatalog.Keep) (*exec.Source, error) {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	activity := source.Address().ObjectID == pgcatalog.StatActivityOID
	live, activities := e.captureActivitiesLocked(scope, activity)
	in := pgcatalog.Input{Context: scope.Op.Context(), Owner: &e.Catalog.Owner, Ref: ref, Source: source, Keep: keep, Catalog: auth.Catalog, Roles: auth.Roles, ActivityObserver: auth.State.CurrentUser, Activities: activities}
	e.Mu.Unlock()
	if activity {
		e.overlayActivityWaits(live, activities)
	}
	produce, _ := pgcatalog.ProducerFor(source.Address().ObjectID)
	return catalogrt.CaptureRows(in, produce)
}

// Manager observations are overlaid only after releasing Engine.Mu, joining
// exact physical tokens and command transactions, never PID-shaped identities.
func (e *Engine) overlayActivityWaits(live, activities map[ids.SessionToken]statistics.Activity) {
	waits := e.Locks.ActivityWaits()
	for token, a := range activities {
		a.WaitType, a.Wait = "", ""
		current, exists := live[token]
		if exists && current.Incarnation == a.Incarnation && current.Active {
			a.WaitType, a.Wait = current.WaitType, current.Wait
			if wait, ok := waits[token]; ok && wait.Transaction == current.Command.Transaction {
				a.WaitType, a.Wait = "Lock", wait.Event
			}
		}
		activities[token] = a
	}
}

// Engine.Mu and transaction admission are held. Activity, SSL and GSSAPI
// initialize the same raw transaction cut, including canonical XID enrichment.
// Capturing SSL or GSSAPI first must not leave a partial cut for pg_stat_activity.
// liveWaits requests a fresh live cut for activity's wait overlay even when the
// transaction cut already exists; SSL and GSSAPI do not allocate that unused cut.
func (e *Engine) captureActivitiesLocked(scope *StatementScope, liveWaits bool) (map[ids.SessionToken]statistics.Activity, map[ids.SessionToken]statistics.Activity) {
	reader := statsReader(scope.Op.Session, scope.Op.EffectiveSettings())
	provider := e.activityProvider()
	if reader.ActivityCaptured() && !liveWaits {
		return nil, reader.Activities(provider)
	}
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
						a.SubxactCount = entry.SubxactCount
						a.SubxactOverflowed = entry.SubxactOverflowed
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
	return live, reader.Activities(provider)
}
