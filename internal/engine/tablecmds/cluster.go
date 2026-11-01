package tablecmds

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/util"
)

type clusterTarget struct {
	ref, index ids.ObjectRef
	named      bool
}

func clusterPermission(auth core.AuthorizationView, ref ids.ObjectRef) error {
	return auth.Check(access.Requirement{Object: ref, Privileges: authorization.MaintainPrivilege})
}

func clusterRemembered(view catalog.Read, parent *catalog.Relation) *catalog.Relation {
	for _, relation := range view.Relations() {
		if relation.SQLIndex != nil && relation.SQLIndex.Parent == parent.Address() && relation.SQLIndex.Clustered {
			return relation
		}
	}
	return nil
}

func clusterTargets(e *core.Engine, s *core.StatementScope, raw *parser.Cluster) ([]clusterTarget, error) {
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	if len(raw.Table.Parts) != 0 {
		ref, err := resolveVacuumReference(e, auth, raw.Table)
		if err == nil {
			d := auth.Catalog.MustRelation(ref.Address)
			if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.MaterializedViewRelation {
				err = core.SQLError("42809", fmt.Sprintf(`"%s" is not a table or materialized view`, d.Name()))
			} else if !table.VacuumHeap(d) {
				err = core.SQLError("0A000", "clustering partitioned relations is unsupported")
			} else {
				err = clusterPermission(auth, ref)
			}
		}
		e.Mu.Unlock()
		if err != nil {
			return nil, err
		}
		return []clusterTarget{{ref: ref, named: true}}, nil
	}
	var targets []clusterTarget
	var warnings []string
	for _, selected := range auth.Catalog.Relations() {
		if selected.SQLIndex == nil || !selected.SQLIndex.Clustered {
			continue
		}
		parent, exists := auth.Catalog.Relation(selected.SQLIndex.Parent)
		if !exists || !table.VacuumHeap(parent) {
			continue
		}
		ref := auth.Ref(parent.Address())
		rights, checkErr := auth.Effective(ref, false)
		if checkErr != nil {
			e.Mu.Unlock()
			return nil, checkErr
		}
		if rights&authorization.MaintainPrivilege == 0 {
			warnings = append(warnings, parent.Name())
			continue
		}
		targets = append(targets, clusterTarget{ref: ref, index: auth.Ref(selected.Address())})
	}
	e.Mu.Unlock()
	for _, name := range warnings {
		analyzeWarning(s, "01000", fmt.Sprintf(`permission denied to cluster "%s", skipping it`, name))
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ref.Address.ObjectID < targets[j].ref.Address.ObjectID })
	return targets, nil
}

// Heap AX uses the ordinary captured operation policy. Index recipes are only
// rebuilt by the ordinary overlay path when their current declarations change.
func ExecuteCluster(e *core.Engine, s *core.StatementScope, raw *parser.Cluster, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	bare := len(raw.Table.Parts) == 0
	if bare && s.Owner.InBlock() {
		return core.Result{}, core.SQLError("25001", "CLUSTER cannot run inside a transaction block")
	}
	targets, err := clusterTargets(e, s, raw)
	if err != nil {
		return core.Result{}, err
	}
	for occurrence, target := range targets {
		if bare && occurrence > 0 {
			if err := util.ExecutionCheckpoint(s.ActivateRuntime(s.Op.Context()), "cluster.database.next"); err != nil {
				return core.Result{}, err
			}
		}
		checkpoint := e.Locks.Checkpoint(s.LockTicket)
		worked, err := clusterRelation(e, s, raw, target, checkpoint)
		if err != nil {
			return core.Result{}, err
		}
		if bare {
			if worked {
				if err := s.CompleteClusterTarget(); err != nil {
					return core.Result{}, err
				}
			} else {
				e.Locks.Restore(s.LockTicket, checkpoint)
			}
		}
	}
	s.CompleteUtility()
	return core.Result{}, nil
}

func clusterRelation(e *core.Engine, s *core.StatementScope, raw *parser.Cluster, target clusterTarget, checkpoint lock.Checkpoint) (bool, error) {
	if err := vacuumAcquire(s, target.ref, lock.AccessExclusive, false); err != nil {
		return false, err
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return false, err
	}
	parent, exists := auth.Catalog.Relation(target.ref.Address)
	if target.ref.Epoch != auth.Catalog.Epoch() {
		e.Mu.Unlock()
		return false, ids.ErrCatalogReplaced
	}
	if !exists {
		e.Mu.Unlock()
		if !target.named {
			return false, nil
		}
		return false, ids.ErrCatalogReplaced
	}
	if !table.VacuumHeap(parent) {
		e.Mu.Unlock()
		return false, ids.ErrCatalogReplaced
	}
	if err = clusterPermission(auth, target.ref); err != nil {
		e.Mu.Unlock()
		if !target.named {
			analyzeWarning(s, "01000", fmt.Sprintf(`permission denied to cluster "%s", skipping it`, parent.Name()))
			return false, nil
		}
		return false, err
	}
	if table.VacuumForeignTemp(auth.Catalog, s.Op.Session.SessionToken, parent) {
		e.Mu.Unlock()
		if !target.named {
			return false, nil
		}
		return false, core.SQLError("0A000", "cannot cluster temporary tables of other sessions")
	}
	if err = s.ClusterInUse(auth.Catalog, parent); err != nil {
		e.Mu.Unlock()
		return false, err
	}
	var selected *catalog.Relation
	if raw.Index.Text != "" {
		selected, err = table.ClusterIndex(auth.Catalog, parent, raw.Index.Text)
	} else {
		selected = clusterRemembered(auth.Catalog, parent)
		if !target.named && (selected == nil || selected.Address() != target.index.Address) {
			e.Mu.Unlock()
			return false, nil
		}
		if selected == nil {
			err = core.SQLError("42704", fmt.Sprintf(`there is no previously clustered index for table "%s"`, parent.Name()))
		} else {
			selected, err = table.ClusterIndex(auth.Catalog, parent, selected.Name())
		}
	}
	if err != nil {
		e.Mu.Unlock()
		return false, err
	}
	unpopulated := parent.RelationKind == catalog.MaterializedViewRelation && !parent.Populated
	if unpopulated {
		e.Mu.Unlock()
		if target.named {
			e.Locks.Restore(s.LockTicket, checkpoint)
		}
		return false, nil
	}
	store := e.PrivateStorageLocked(s.PendingCatalog(), parent.Address())
	if store == nil {
		e.Mu.Unlock()
		return false, ids.ErrCatalogReplaced
	}
	s.Catalog = s.PendingCatalog().Clone()
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, s.Op.Session)
	stage.DomainBases = append(stage.DomainBases, parent, selected)
	s.LockedRef, s.LockedDescriptor, s.LockedStorage = target.ref, parent, store
	_, err = table.ClusterCandidate(core.DDLHost{E: e, Op: s.Op, CatalogStage: stage}, parent, parent, parser.AlterTableAction{Action: parser.ClusterOn, ClusterIndex: parser.Identifier{Text: selected.Name()}})
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return false, err
	}
	err = s.WithMaintenanceOwner(parent, func(evaluation *exec.Context) error {
		if err := refreshClusterIndexRecipes(e, evaluation, parent, stage, s.PendingCatalog()); err != nil {
			return err
		}
		if err := util.ExecutionCheckpoint(s.ActivateRuntime(s.Op.Context()), "cluster.publish"); err != nil {
			return err
		}
		prepared, err := e.ValidateCatalogStage(s.Op.Context(), stage)
		if err == nil {
			_, err = e.PublishCatalogOperation(s.Op, s, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
		}
		return err
	})
	return err == nil, err
}
