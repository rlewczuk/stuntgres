package engine

import (
	"context"
	"fmt"
	"sort"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// A selected candidate retains identity and diagnostic origin, not permission,
// shape or storage authority. Anonymous descendants/omitted candidates have no
// original name and silently skip an unavailable or disappeared work target.
type vacuumTarget struct {
	ref  ids.ObjectRef
	name string
}

// Heap authority is captured only after the target's work lock and fresh gates.
// It remains fixed during detached work and final publication validation.
type vacuumHeapTarget struct {
	ref      ids.ObjectRef
	relation *catalog.Relation
	store    *storage.Relation
}

func vacuumNotice(s *StatementScope, code, message string) {
	s.op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message})
}
func vacuumAllowed(ctx context.Context, auth AuthorizationView, d *catalog.Relation) (bool, error) {
	if actor, ok := auth.Roles.Find(auth.State.CurrentUser); ok && actor.Superuser {
		return true, nil
	}
	principals, err := auth.Roles.RoleClosure(ctx, auth.State.CurrentUser, true)
	if err != nil {
		return false, err
	}
	var databaseOwner authorization.PrincipalRef
	if !catalog.SharedRelation(d) {
		if database, ok := auth.Catalog.Database(catalog.CurrentDatabaseAddress); ok {
			databaseOwner = database.Owner
		}
	}
	for _, principal := range principals {
		if principal == d.Owner || databaseOwner != (authorization.PrincipalRef{}) && principal == databaseOwner {
			return true, nil
		}
	}
	rights, err := auth.Effective(auth.Ref(d.Address()), false)
	return rights&authorization.MaintainPrivilege != 0, err
}
func vacuumHeap(d *catalog.Relation) bool {
	return d.Partition == nil && !d.VirtualView && d.Foreign == nil && (d.RelationKind == catalog.TableRelation || d.RelationKind == catalog.MaterializedViewRelation)
}
func vacuumForeignTemp(auth AuthorizationView, d *catalog.Relation) bool {
	ns, ok := auth.Catalog.Namespace(d.Namespace())
	return ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != auth.session.SessionToken
}
func (s *StatementScope) vacuumAcquire(ref ids.ObjectRef, mode lock.Mode, skip bool) error {
	e := s.engine()
	policy := lock.Policy{NoWait: skip, TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, mode, policy)
	if request != nil {
		err = request.Wait()
	}
	return err
}

// VACUUM needs only namespace/object authority for a peer TEMP relation, never
// access to its data. Keep ordinary SELECT/DML's stricter foreign-TEMP lookup
// boundary unchanged; this utility path still checks the actual namespace ACL.
func (e *Engine) resolveVacuumReference(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	if err := e.catalog.validateCatalogViewLocked(auth.Catalog); err != nil {
		return ids.ObjectRef{}, err
	}
	if len(name.Parts) == 1 {
		// Share parsing, aliases, precedence, dedup and ACL omission with the
		// ordinary path builder. Only this maintenance caller permits peer TEMP.
		path, _, err := explicitPathForPolicy(auth, analyze.RelationLookup, true)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		return resolveRelationPathSnapshot(auth, name, analyze.NamespaceSelection{Path: effectivePath(auth, path, analyze.RelationLookup)})
	}
	if len(name.Parts) == 2 && name.Parts[0].Text != "pg_temp" {
		address := auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
		ns, ok := auth.definitionNamespace(address)
		if ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != auth.session.SessionToken {
			if err := auth.CanUse(auth.Ref(address)); err != nil {
				return ids.ObjectRef{}, err
			}
			return resolveRelationPathSnapshot(auth, name, analyze.NamespaceSelection{Qualified: address})
		}
	}
	return e.resolveRelationReference(auth, name, true)
}

func vacuumChildren(view catalog.Read, parent ids.ObjectAddress) []*catalog.Relation {
	children := catalog.InheritanceChildren(view, parent)
	partitionChildren := false
	for _, descriptor := range view.Descriptors() {
		if child, ok := descriptor.(*catalog.Relation); ok && child.Membership != nil && child.Membership.Parent == parent {
			children = append(children, child)
			partitionChildren = true
		}
	}
	if partitionChildren {
		sort.Slice(children, func(i, j int) bool { return children[i].Address().ObjectID < children[j].Address().ObjectID })
	}
	return children
}

func (e *Engine) vacuumTargets(s *StatementScope, raw *parser.Vacuum, skipLocked bool) ([]vacuumTarget, error) {
	var targets []vacuumTarget
	type selectionWarning struct{ code, message string }
	var warnings []selectionWarning
	// All expansions finish before work. Deliver only genuine selection notices,
	// in encounter order and outside every checkpoint/metadata mutex region.
	defer func() {
		for _, warning := range warnings {
			vacuumNotice(s, warning.code, warning.message)
		}
	}()
	appendRoot := func(auth AuthorizationView, d *catalog.Relation, name string) error {
		allowed, err := vacuumAllowed(s.op.Context(), auth, d)
		if err != nil {
			return err
		}
		if allowed {
			targets = append(targets, vacuumTarget{auth.Ref(d.Address()), name})
		} else {
			warnings = append(warnings, selectionWarning{"01000", fmt.Sprintf(`permission denied to vacuum "%s", skipping it`, d.Name())})
		}
		return nil
	}
	if len(raw.Targets) == 0 {
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err := e.authorizationLocked(s.op)
		if err != nil {
			return nil, err
		}
		if s.txRef.Epoch() != auth.Catalog.Epoch() {
			return nil, ids.ErrCatalogReplaced
		}
		for _, descriptor := range auth.Catalog.Descriptors() {
			// Native provider catalogs are not fabricated physical heaps.
			if d, ok := descriptor.(*catalog.Relation); ok && (vacuumHeap(d) || d.Partition != nil) {
				if err := appendRoot(auth, d, ""); err != nil {
					return nil, err
				}
			}
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].ref.Address.ObjectID < targets[j].ref.Address.ObjectID })
		return targets, nil
	}
	for _, target := range raw.Targets {
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		var ref ids.ObjectRef
		if err == nil {
			ref, err = e.resolveVacuumReference(auth, target.Name)
		}
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		name := target.Name.Parts[len(target.Name.Parts)-1].Text
		checkpoint := e.locks.Checkpoint(s.ticket)
		err = s.vacuumAcquire(ref, lock.AccessShare, skipLocked)
		if err == nil {
			e.mu.Lock()
			auth, err = e.authorizationLocked(s.op)
			if err == nil {
				current, lookupErr := e.resolveVacuumReference(auth, target.Name)
				d, ok := auth.Catalog.Relation(ref.Address)
				if lookupErr != nil || current != ref || !ok || ref.Epoch != auth.Catalog.Epoch() {
					err = ids.ErrCatalogReplaced
				} else {
					err = appendRoot(auth, d, name)
					if err == nil && target.Only && d.Partition != nil {
						warnings = append(warnings, selectionWarning{"01000", fmt.Sprintf(`VACUUM ONLY of partitioned table "%s" has no effect`, d.Name())})
					}
					if err == nil && !target.Only {
						// Breadth-first, direct children OID-ordered. Dedup only
						// this root's expansion, never overlapping textual roots.
						queue := vacuumChildren(auth.Catalog, d.Address())
						if len(queue) != 0 {
							seen := map[ids.ObjectAddress]bool{d.Address(): true}
							// The bound grows as descendants are appended; a
							// fixed-length range would truncate this BFS.
							for head := 0; head < len(queue); head++ {
								child := queue[head]
								if seen[child.Address()] {
									continue
								}
								seen[child.Address()] = true
								targets = append(targets, vacuumTarget{auth.Ref(child.Address()), ""})
								queue = append(queue, vacuumChildren(auth.Catalog, child.Address())...)
							}
						}
					}
				}
			}
			e.mu.Unlock()
		}
		e.locks.Restore(s.ticket, checkpoint)
		if err == lock.ErrUnavailable && skipLocked {
			warnings = append(warnings, selectionWarning{"55P03", fmt.Sprintf(`skipping vacuum of "%s" --- lock not available`, name)})
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return targets, nil
}
func (e *Engine) executeVacuum(s *StatementScope, raw *parser.Vacuum, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	options, err := normalizeVacuum(raw)
	if err != nil {
		return Result{}, err
	}
	if s.owner.InBlock() {
		return Result{}, sqlError("25001", "VACUUM cannot run inside a transaction block")
	}
	if options.full {
		return Result{}, sqlError("0A000", "VACUUM FULL is not supported")
	}
	if options.analyze {
		return Result{}, sqlError("0A000", "VACUUM ANALYZE is not supported")
	}
	if options.onlyDatabase {
		e.mu.Lock()
		defer e.mu.Unlock()
		if err := e.vacuumDatabasePreflightLocked(s); err != nil {
			return Result{}, err
		}
		e.updateDatabaseMaintenanceLocked()
		s.completed = true
		return Result{}, nil
	}
	targets, err := e.vacuumTargets(s, raw, options.skipLocked)
	if err != nil {
		return Result{}, err
	}
	for _, target := range targets {
		checkpoint := e.locks.Checkpoint(s.ticket)
		err = func() error {
			defer e.locks.Restore(s.ticket, checkpoint)
			if err := s.vacuumAcquire(target.ref, lock.ShareUpdateExclusive, options.skipLocked); err != nil {
				if err == lock.ErrUnavailable && options.skipLocked {
					if target.name != "" {
						vacuumNotice(s, "55P03", fmt.Sprintf(`skipping vacuum of "%s" --- lock not available`, target.name))
					}
					return nil
				}
				return err
			}
			return e.vacuumRelation(s, target, options)
		}()
		if err != nil {
			return Result{}, err
		}
	}
	if !options.skipDatabase {
		e.mu.Lock()
		err = e.vacuumDatabasePreflightLocked(s)
		if err == nil {
			e.updateDatabaseMaintenanceLocked()
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
	}
	s.completed = true
	return Result{}, nil
}
