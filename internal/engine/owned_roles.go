package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeOwned(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return fail(err)
	}
	plan, err := role.PlanOwned(auth.View, stmt, func(address ids.ObjectAddress) string {
		path, _ := auth.ReferencePath()
		return dependencyObject(auth.Catalog, address, path)
	})
	if err != nil {
		return fail(err)
	}
	if plan.Noop {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	reassign, sources, target, mode, roots, primaryRoot := plan.Reassign, plan.Sources, plan.Target, plan.Mode, plan.Roots, plan.PrimaryRoot
	stage := e.ownershipStage(auth, scope.op)
	stage.dependencyPath, _ = e.referencePath(auth)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	// Pin roots and automatic associations before producing private candidates.
	for _, a := range access.OwnershipRelationLocks(stage.Base, roots) {
		if err = scope.op.Context().Err(); err != nil {
			return Result{}, err
		}
		ref := e.catalog.Domain.Ref(stage.Base.Epoch(), a)
		if err = scope.acquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	providersChanged := false
	if reassign {
		for _, a := range roots {
			d := stage.Base.Get(a)
			owner, _, _ := access.DescriptorACL(d)
			if owner == target {
				continue
			}
			// PostgreSQL's schema and generic collation handlers retain their
			// own eligibility checks; relation/type bulk recursion does not SET.
			switch d.(type) {
			case *catalog.ForeignObject:
				foreign := d.(*catalog.ForeignObject)
				err = fdw.OwnershipEligibility(auth.View, auth, foreign, target)
				if err == nil && foreign.Address().ClassID == ids.ForeignServerClassID {
					stage.domainBases = append(stage.domainBases, auth.Catalog.Get(foreign.Wrapper))
				}
			case *catalog.Namespace:
				err = auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(stage.Base.Epoch(), a), OwnerOnly: true})
				if err == nil {
					err = access.OwnershipSuccessor(auth.View, target)
				}
				if err == nil {
					err = access.OwnershipDatabaseCreate(auth.View, auth)
				}
			case *catalog.Collation, *catalog.Conversion, *catalog.Operator, *catalog.Database, *catalog.Extension, *catalog.Procedure, *catalog.TSDictionary:
				err = auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(stage.Base.Epoch(), a), OwnerOnly: true})
				if err == nil {
					err = access.OwnershipEligibility(auth.View, auth, target, d.Namespace())
				}
			}
			if err != nil {
				break
			}
			if err = role.StageOwnership(stage, d, target); err != nil {
				break
			}
			switch d.(type) {
			case *catalog.Procedure, *catalog.Operator, *catalog.TSDictionary:
				providersChanged = true
			}
		}
		if err == nil && providersChanged {
			candidate := e.stageRead(stage).State()
			err = stored.ReownExpressions(e.catalog.read(candidate), candidate, nil, false)
			if err == nil {
				for a, d := range candidate.Descriptors() {
					if old := stage.Get(a); old != nil {
						if old == d {
							continue
						}
					} else if stage.Base.Get(a) == d {
						continue
					}
					if err = stage.Replace(d); err != nil {
						break
					}
				}
			}
		}
	} else {
		for _, a := range roots {
			if err = stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), a), a.ClassID == ids.NamespaceClassID); err != nil {
				break
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if !reassign {
		// PostgreSQL collects actual policy and grantor-membership deletions
		// alongside ownership roots. Prepare cleanup once, still entirely private,
		// before discovering consumers of the complete pending deletion union.
		if err = role.StageOwnedCleanup(scope.op.Context(), stage, auth.Roles, auth.RoleRecord, sources); err != nil {
			return Result{}, err
		}
		if primaryRoot != (ids.ObjectAddress{}) && role.OwnedCleanupAddsDeletionRoot(stage, sources) {
			primaryRoot = ids.ObjectAddress{}
		}
		if err = scope.stageExpressionDependents(stage, roots, mode, nil); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "2BP01" {
				if primaryRoot != (ids.ObjectAddress{}) {
					diagnostic.Message = "cannot drop " + dependencyObject(stage.Base, primaryRoot, stage.dependencyPath) + " because other objects depend on it"
				} else {
					diagnostic.Message = "cannot drop desired object(s) because other objects depend on them"
				}
			}
			return Result{}, err
		}
		if err = scope.stageOwnedSequences(stage, roots, mode, nil); err != nil {
			return Result{}, err
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	result, err := e.publishOwnership(scope, stage)
	if err == nil && !reassign && mode == parser.DropCascade && len(stage.dropCascadeNames) > 0 {
		message, detail := stage.dropCascadeNames[0], ""
		if len(stage.dropCascadeNames) > 1 {
			message = fmt.Sprintf("drop cascades to %d other objects", len(stage.dropCascadeNames))
			detail = strings.Join(stage.dropCascadeNames, "\n")
		}
		scope.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
	}
	return result, err
}
