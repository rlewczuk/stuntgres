package rolecmds

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ExecuteOwned runs REASSIGN OWNED and DROP OWNED in one ownership stage.
func ExecuteOwned(e *core.Engine, scope *core.StatementScope, stmt parser.Statement, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return fail(err)
	}
	plan, err := role.PlanOwned(auth.View, stmt, func(address ids.ObjectAddress) string {
		path, _ := auth.ReferencePath()
		return stored.DependencyObject(auth.Catalog, address, path)
	})
	if err != nil {
		return fail(err)
	}
	if plan.Noop {
		e.Mu.Unlock()
		scope.CompleteUtility()
		return core.Result{}, scope.Op.Context().Err()
	}
	reassign, sources, mode, roots, primaryRoot := plan.Reassign, plan.Sources, plan.Mode, plan.Roots, plan.PrimaryRoot
	stage := core.OwnershipStage(e, auth, scope.Op)
	stage.DependencyPath, _ = core.ReferencePath(auth)
	if !reassign {
		for _, address := range roots {
			stage.MarkDrop(address, catalog.DropProvenance{Original: true})
		}
	}
	e.Mu.Unlock()
	defer func() { e.DiscardCatalogStage(stage) }()
	// Pin roots/associations and actual policy-deletion survivors in one
	// canonical order before producing private candidates.
	var policySources = sources
	if reassign {
		policySources = nil
	}
	for _, a := range access.OwnershipRelationLocks(stage.Base, roots, policySources) {
		if err = scope.Op.Context().Err(); err != nil {
			return core.Result{}, err
		}
		ref := e.Catalog.Domain.Ref(stage.Base.Epoch(), a)
		if err = scope.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return core.Result{}, err
		}
	}
	objectLocked := false
	// Global roots use the same class/address order as definition mutations.
	// OwnedRoots already orders object IDs within each class.
	for _, class := range [...]ids.OID{ids.LanguageClassID, ids.SubscriptionClassID, ids.PublicationClassID} {
		for _, address := range roots {
			if address.ClassID != class {
				continue
			}
			ref := e.Catalog.Domain.Ref(stage.Base.Epoch(), address)
			if err = scope.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
				return core.Result{}, err
			}
			objectLocked = true
		}
	}
	e.Mu.Lock()
	if objectLocked {
		// Admission may have waited for a peer ALTER. Reauthorize the same
		// root set before staging; language definitions retain their captured
		// predecessors rather than silently adopting changed execution routes.
		fresh, failure := e.AuthorizationLocked(scope.Op)
		if failure != nil {
			return fail(failure)
		}
		if fresh.Catalog.Epoch() != stage.Base.Epoch() {
			return fail(ids.ErrCatalogReplaced)
		}
		for _, address := range roots {
			if address.ClassID == ids.LanguageClassID && fresh.Catalog.Get(address) != stage.Base.Get(address) {
				return fail(ids.ErrCatalogReplaced)
			}
		}
		current, failure := role.PlanOwned(fresh.View, stmt, func(address ids.ObjectAddress) string {
			path, _ := fresh.ReferencePath()
			return stored.DependencyObject(fresh.Catalog, address, path)
		})
		if failure != nil {
			return fail(failure)
		}
		if current.Target != plan.Target || !maps.Equal(current.Sources, sources) || !slices.Equal(current.Roots, roots) {
			return fail(catalog.Conflict{})
		}
		e.DiscardCatalogStage(stage)
		auth, plan = fresh, current
		stage = core.OwnershipStage(e, auth, scope.Op)
		stage.DependencyPath, _ = core.ReferencePath(auth)
		if !reassign {
			for _, address := range roots {
				stage.MarkDrop(address, catalog.DropProvenance{Original: true})
			}
		}
	}
	err = role.StageOwned(RoleHost{core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, plan)
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if !reassign {
		provider := core.StageDropProvider(scope.Op.Context(), stage, scope, nil, nil)
		raw, failure := provider.PrepareSelectedRawDrops()
		if failure != nil {
			return core.Result{}, failure
		}
		cleanup, failure := role.PrepareOwnedCleanup(scope.Op.Context(), stage, auth.Roles, auth.RoleRecord, sources, raw)
		if failure != nil {
			return core.Result{}, failure
		}
		// Canonical raw effects and cleanup-added roots form the same genuine
		// pending union. Neither metadata capture changes catalog liveness.
		pending := slices.Clone(roots)
		for _, address := range cleanup.DeletionRoots() {
			if !slices.Contains(pending, address) {
				pending = append(pending, address)
			}
			stage.MarkDrop(address, catalog.DropProvenance{Original: true})
		}
		for _, effect := range raw.Effects() {
			if effect.Remove.Address != (ids.ObjectAddress{}) && !slices.Contains(pending, effect.Remove.Address) {
				pending = append(pending, effect.Remove.Address)
			}
		}
		cleanupSource := stage.StageRead()
		for effect := range cleanup.Effects() {
			if effect.Remove.Address != (ids.ObjectAddress{}) && !slices.Contains(pending, effect.Remove.Address) {
				pending = append(pending, effect.Remove.Address)
			}
			next, ok := effect.Descriptor.(*catalog.Relation)
			if !ok {
				continue
			}
			before, ok := cleanupSource.Get(next.Address()).(*catalog.Relation)
			if !ok {
				return core.Result{}, ids.ErrCatalogReplaced
			}
			records, failure := provider.CaptureRelationProjection(before, next)
			if failure != nil {
				return core.Result{}, failure
			}
			for _, record := range records {
				if record.Address.ClassID == ids.PolicyClassID {
					stage.MarkDrop(record.Address, catalog.DropProvenance{Original: true})
					if !slices.Contains(pending, record.Address) {
						pending = append(pending, record.Address)
					}
				}
			}
			stage.DropRecords = append(stage.DropRecords, records...)
		}
		if cleanup.AddsDeletionRoot() {
			primaryRoot = ids.ObjectAddress{}
		}
		dependencies, failure := provider.PrepareExpressionDependencies(pending, mode, ids.ObjectAddress{})
		if failure != nil {
			primary := ""
			if primaryRoot != (ids.ObjectAddress{}) {
				primary = stored.DependencyObject(stage.Base, primaryRoot, stage.DependencyPath)
			}
			return core.Result{}, role.OwnedDependentsError(failure, primary)
		}
		if err = stage.ApplySelectedDropRoots(dependencies); err != nil {
			return core.Result{}, err
		}
		e.Mu.Lock()
		err = e.EvolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil)
		e.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
		if err = cleanup.Apply(scope.Op.Context(), stage); err != nil {
			return core.Result{}, err
		}
		if err = provider.ApplyExpressionDependencies(dependencies); err != nil {
			return core.Result{}, err
		}
		if err = provider.ApplyOwnedExpressionDependencies(dependencies); err != nil {
			return core.Result{}, err
		}
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	result, err := core.PublishOwnership(e, scope, stage)
	if err == nil && !reassign && mode == parser.DropCascade && len(stage.DropCascadeNames) > 0 {
		message, detail := stage.DropCascadeNames[0], ""
		if len(stage.DropCascadeNames) > 1 {
			message = fmt.Sprintf("drop cascades to %d other objects", len(stage.DropCascadeNames))
			detail = strings.Join(stage.DropCascadeNames, "\n")
		}
		scope.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
	}
	return result, err
}
