package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Heap authority is captured only after the target's work lock and fresh gates.
// It remains fixed during detached work and final publication validation.
type vacuumHeapTarget struct {
	ref      ids.ObjectRef
	relation *catalog.Relation
	store    *storage.Relation
}

func vacuumNotice(s *StatementScope, w VacuumWarning) {
	s.op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: w.Code, Message: w.Message})
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
		in := pathInput(auth, true)
		path, _, err := analyze.ExplicitPath(in, analyze.RelationLookup, true)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		return resolveRelationPathSnapshot(auth, name, analyze.NamespaceSelection{Path: analyze.EffectivePath(in, path, analyze.RelationLookup)})
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

func (e *Engine) vacuumTargets(s *StatementScope, raw *parser.Vacuum, skipLocked bool) ([]VacuumTarget, error) {
	var selection VacuumSelection
	// All expansions finish before work. Deliver only genuine selection notices,
	// in encounter order and outside every checkpoint/metadata mutex region.
	defer func() {
		for _, warning := range selection.Warnings {
			vacuumNotice(s, warning)
		}
	}()
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
		if err := selection.AddDatabase(s.op.Context(), auth.View, auth); err != nil {
			return nil, err
		}
		return selection.Targets, nil
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
					err = selection.AddRoot(s.op.Context(), auth.View, auth, d, name, target.Only)
				}
			}
			e.mu.Unlock()
		}
		e.locks.Restore(s.ticket, checkpoint)
		if err == lock.ErrUnavailable && skipLocked {
			selection.LockUnavailable(name)
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return selection.Targets, nil
}
func (e *Engine) executeVacuum(s *StatementScope, raw *parser.Vacuum, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	options, err := NormalizeVacuum(raw)
	if err != nil {
		return Result{}, err
	}
	if err := CheckVacuumEntry(options, s.owner.InBlock()); err != nil {
		return Result{}, err
	}
	if options.OnlyDatabase {
		e.mu.Lock()
		defer e.mu.Unlock()
		if err := e.vacuumDatabasePreflightLocked(s); err != nil {
			return Result{}, err
		}
		e.updateDatabaseMaintenanceLocked()
		s.completed = true
		return Result{}, nil
	}
	targets, err := e.vacuumTargets(s, raw, options.SkipLocked)
	if err != nil {
		return Result{}, err
	}
	for _, target := range targets {
		checkpoint := e.locks.Checkpoint(s.ticket)
		err = func() error {
			defer e.locks.Restore(s.ticket, checkpoint)
			if err := s.vacuumAcquire(target.Ref, lock.ShareUpdateExclusive, options.SkipLocked); err != nil {
				if err == lock.ErrUnavailable && options.SkipLocked {
					if target.Name != "" {
						vacuumNotice(s, VacuumLockWarning(target.Name))
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
	if !options.SkipDatabase {
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
