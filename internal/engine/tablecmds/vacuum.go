package tablecmds

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
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

func vacuumNotice(s *core.StatementScope, w table.VacuumWarning) {
	s.Op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: w.Code, Message: w.Message})
}
func vacuumAcquire(s *core.StatementScope, ref ids.ObjectRef, mode lock.Mode, skip bool) error {
	e := s.Engine()
	policy := lock.Policy{NoWait: skip, TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref}, mode, policy)
	if request != nil {
		err = request.Wait()
	}
	return err
}

// VACUUM needs only namespace/object authority for a peer TEMP relation, never
// access to its data. Keep ordinary SELECT/DML's stricter foreign-TEMP lookup
// boundary unchanged; this utility path still checks the actual namespace ACL.
func resolveVacuumReference(e *core.Engine, auth core.AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	if err := e.Catalog.ValidateCatalogViewLocked(auth.Catalog); err != nil {
		return ids.ObjectRef{}, err
	}
	if len(name.Parts) == 1 {
		// Share parsing, aliases, precedence, dedup and ACL omission with the
		// ordinary path builder. Only this maintenance caller permits peer TEMP.
		in := core.PathInput(auth, true)
		path, _, err := analyze.ExplicitPath(in, analyze.RelationLookup, true)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		return core.ResolveRelationPathSnapshot(auth, name, analyze.NamespaceSelection{Path: analyze.EffectivePath(in, path, analyze.RelationLookup)})
	}
	if len(name.Parts) == 2 && name.Parts[0].Text != "pg_temp" {
		address := auth.DefinitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
		ns, ok := definitionNamespace(auth, address)
		if ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != auth.Session.SessionToken {
			if err := auth.CanUse(auth.Ref(address)); err != nil {
				return ids.ObjectRef{}, err
			}
			return core.ResolveRelationPathSnapshot(auth, name, analyze.NamespaceSelection{Qualified: address})
		}
	}
	return e.ResolveRelationReference(auth, name, true)
}

func vacuumTargets(e *core.Engine, s *core.StatementScope, raw *parser.Vacuum, skipLocked bool) ([]table.VacuumTarget, error) {
	var selection table.VacuumSelection
	// All expansions finish before work. Deliver only genuine selection notices,
	// in encounter order and outside every checkpoint/metadata mutex region.
	defer func() {
		for _, warning := range selection.Warnings {
			vacuumNotice(s, warning)
		}
	}()
	if len(raw.Targets) == 0 {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(s.Op)
		if err != nil {
			return nil, err
		}
		if s.TxRef.Epoch() != auth.Catalog.Epoch() {
			return nil, ids.ErrCatalogReplaced
		}
		if err := selection.AddDatabase(s.Op.Context(), auth.View, auth); err != nil {
			return nil, err
		}
		return selection.Targets, nil
	}
	for _, target := range raw.Targets {
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(s.Op)
		var ref ids.ObjectRef
		if err == nil {
			ref, err = resolveVacuumReference(e, auth, target.Name)
		}
		e.Mu.Unlock()
		if err != nil {
			return nil, err
		}
		name := target.Name.Parts[len(target.Name.Parts)-1].Text
		checkpoint := e.Locks.Checkpoint(s.LockTicket)
		err = vacuumAcquire(s, ref, lock.AccessShare, skipLocked)
		if err == nil {
			e.Mu.Lock()
			auth, err = e.AuthorizationLocked(s.Op)
			if err == nil {
				current, lookupErr := resolveVacuumReference(e, auth, target.Name)
				d, ok := auth.Catalog.Relation(ref.Address)
				if lookupErr != nil || current != ref || !ok || ref.Epoch != auth.Catalog.Epoch() {
					err = ids.ErrCatalogReplaced
				} else {
					err = selection.AddRoot(s.Op.Context(), auth.View, auth, d, name, target.Only)
				}
			}
			e.Mu.Unlock()
		}
		e.Locks.Restore(s.LockTicket, checkpoint)
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

// ExecuteVacuum runs VACUUM in the statement scope: target selection, then
// each target under its own lock checkpoint, then the database horizon. It
// takes Engine.Mu only in short windows and holds nothing across lock waits.
func ExecuteVacuum(e *core.Engine, s *core.StatementScope, raw *parser.Vacuum, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	options, err := table.NormalizeVacuum(raw)
	if err != nil {
		return core.Result{}, err
	}
	if err := table.CheckVacuumEntry(options, s.Owner.InBlock()); err != nil {
		return core.Result{}, err
	}
	if options.OnlyDatabase {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		if err := vacuumDatabasePreflightLocked(e, s); err != nil {
			return core.Result{}, err
		}
		e.UpdateDatabaseMaintenanceLocked()
		s.Completed = true
		return core.Result{}, nil
	}
	targets, err := vacuumTargets(e, s, raw, options.SkipLocked)
	if err != nil {
		return core.Result{}, err
	}
	for _, target := range targets {
		checkpoint := e.Locks.Checkpoint(s.LockTicket)
		err = func() error {
			defer e.Locks.Restore(s.LockTicket, checkpoint)
			if err := vacuumAcquire(s, target.Ref, lock.ShareUpdateExclusive, options.SkipLocked); err != nil {
				if err == lock.ErrUnavailable && options.SkipLocked {
					if target.Name != "" {
						vacuumNotice(s, table.VacuumLockWarning(target.Name))
					}
					return nil
				}
				return err
			}
			return vacuumRelation(e, s, target, options)
		}()
		if err != nil {
			return core.Result{}, err
		}
	}
	if !options.SkipDatabase {
		e.Mu.Lock()
		err = vacuumDatabasePreflightLocked(e, s)
		if err == nil {
			e.UpdateDatabaseMaintenanceLocked()
		}
		e.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
	}
	s.Completed = true
	return core.Result{}, nil
}

func definitionNamespace(a core.AuthorizationView, address ids.ObjectAddress) (*catalog.Namespace, bool) {
	return catalog.DefinitionNamespaceAt(a.Catalog, a.Definition, address)
}
