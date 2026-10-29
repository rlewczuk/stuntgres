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
		// PostgreSQL collects actual policy and grantor-membership deletions
		// alongside ownership roots. Prepare cleanup once, still entirely private,
		// before discovering consumers of the complete pending deletion union.
		if err = role.StageOwnedCleanup(scope.Op.Context(), stage, auth.Roles, auth.RoleRecord, sources); err != nil {
			return core.Result{}, err
		}
		provider := core.StageDropProvider(scope.Op.Context(), stage, scope, nil, nil)
		for address, descriptor := range stage.Descriptors() {
			next, ok := descriptor.(*catalog.Relation)
			before, existed := stage.Base.Relation(address)
			if !ok || !existed {
				continue
			}
			records, failure := provider.CaptureRelationProjection(before, next)
			if failure != nil {
				return core.Result{}, failure
			}
			for _, record := range records {
				if record.Address.ClassID == ids.PolicyClassID {
					stage.MarkDrop(record.Address, catalog.DropProvenance{Original: true})
				}
			}
			stage.DropRecords = append(stage.DropRecords, records...)
		}
		if primaryRoot != (ids.ObjectAddress{}) && role.OwnedCleanupAddsDeletionRoot(stage, sources) {
			primaryRoot = ids.ObjectAddress{}
		}
		if err = scope.StageExpressionDependents(stage, roots, mode, nil); err != nil {
			primary := ""
			if primaryRoot != (ids.ObjectAddress{}) {
				primary = stored.DependencyObject(stage.Base, primaryRoot, stage.DependencyPath)
			}
			return core.Result{}, role.OwnedDependentsError(err, primary)
		}
		if err = scope.StageOwnedSequences(stage, roots, mode, nil); err != nil {
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
