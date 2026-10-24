package engine

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// This is the ordinary ticket/request lifecycle, including savepoint checkpoints
// and replacement fencing. No manager call or wait is made under Engine.mu.
func (s *StatementScope) acquireTransactionResource(resource lock.Resource, mode lock.Mode) error {
	e := s.engine()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, resource, mode, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	if s.implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	return nil
}

func (s *StatementScope) acquireCatalogResource(resource lock.Resource, mode lock.Mode) error {
	if err := s.acquireTransactionResource(resource, mode); err != nil {
		return err
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if resource.Domain() != e.catalog.Domain || resource.Epoch != s.txRef.Epoch() || resource.Epoch != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return nil
}

// Claim only changed destinations, not reads or vacated names. Acquisition is
// in pure class/namespace/name order. Diagnostic precedence is selected only
// after the entire group is held and current authority is recaptured once.
func (s *StatementScope) acquireCatalogNames(stage *CatalogStage) error {
	if stage.namesAdmitted {
		return nil
	}
	var claimScratch [8]catalog.BindingClaim
	var namespaceScratch [8]ids.ObjectAddress
	claims := claimScratch[:0]
	namespaces := namespaceScratch[:0]
	for key, address := range stage.Names() {
		if key.ClassID != ids.NamespaceClassID && key.ClassID != ids.RelationClassID && key.ClassID != ids.TypeClassID || key.Signature != "" || stage.Base.NameAddress(key) == address {
			continue
		}
		priority := 1
		if key.ClassID == ids.TypeClassID {
			priority = 0
			if typ, ok := stage.Get(address).(*catalog.Type); ok && typ.SQLType.IsArray() {
				priority = 2
			}
		} else if key.ClassID == ids.NamespaceClassID {
			priority = 3
		}
		claim := catalog.BindingClaim{Key: key, Address: address, Priority: priority}
		if typ, ok := stage.Get(address).(*catalog.Type); ok && stage.Base.Get(address) != nil && typ.OwningRelation != (ids.ObjectAddress{}) {
			claim.RelationRename = true
		}
		claims = append(claims, claim)
		if key.Namespace != (ids.ObjectAddress{}) && stage.Base.Get(key.Namespace) != nil {
			namespaces = append(namespaces, key.Namespace)
		}
	}
	if len(claims) == 0 {
		return nil
	}
	slices.SortFunc(namespaces, func(a, b ids.ObjectAddress) int { return cmp.Compare(a.ObjectID, b.ObjectID) })
	namespaces = slices.Compact(namespaces)
	for _, address := range namespaces {
		if err := s.acquireCatalogNamespace(stage.Base, address, stage.creationName); err != nil {
			return err
		}
	}
	slices.SortFunc(claims, func(a, b catalog.BindingClaim) int {
		if n := cmp.Compare(a.Key.ClassID, b.Key.ClassID); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Key.Namespace.ObjectID, b.Key.Namespace.ObjectID); n != 0 {
			return n
		}
		return cmp.Compare(a.Key.Name, b.Key.Name)
	})
	for _, claim := range claims {
		resource := lock.Resource{ObjectRef: stage.owner.catalog.Domain.Ref(stage.Base.Epoch(), claim.Key.Namespace), Name: claim.Key}
		if err := s.acquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
	}
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err == nil && auth.Catalog.Epoch() != stage.Base.Epoch() {
		err = ids.ErrCatalogReplaced
	}
	for priority := 0; priority < 4 && err == nil; priority++ {
		for _, claim := range claims {
			if claim.Priority == priority {
				err = catalog.BindingCurrent(stage.Base, auth.Catalog, claim)
				if err != nil {
					break
				}
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	for _, claim := range claims {
		if claim.Key.ClassID == ids.RelationClassID && stage.Base.Get(claim.Address) == nil {
			ref := stage.owner.catalog.Domain.Ref(stage.Base.Epoch(), claim.Address)
			if err := s.acquireSequence(ref, lock.AccessExclusive, true); err != nil {
				return err
			}
		}
	}
	stage.namesAdmitted = true
	return nil
}

func (s *StatementScope) acquireCatalogNamespace(base catalog.Read, address ids.ObjectAddress, name parser.ObjectName) error {
	ref := s.engine().catalog.Domain.Ref(base.Epoch(), address)
	if err := s.acquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		return err
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(s.op)
	if err == nil && auth.Catalog.Get(address) == nil {
		err = ddl.CreationError(sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, base.Get(address).Name())), name)
	}
	return err
}

func (s *StatementScope) acquireCatalogBinding(base catalog.Read, claim catalog.BindingClaim) error {
	key := claim.Key
	resource := lock.Resource{ObjectRef: s.engine().catalog.Domain.Ref(base.Epoch(), key.Namespace), Name: key}
	if err := s.acquireCatalogResource(resource, lock.AccessExclusive); err != nil {
		return err
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return err
	}
	if auth.Catalog.Epoch() != base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return catalog.BindingCurrent(base, auth.Catalog, claim)
}

// Schema removal protects the captured namespace before constructing its
// dependency closure. Rename/owner changes deliberately do not take this AX.
func (s *StatementScope) acquireSchemaDrop(stmt *parser.DropSchema) (ddl.SchemaDropManifest, error) {
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	var manifest ddl.SchemaDropManifest
	if err == nil {
		manifest, err = ddl.CaptureSchemaDrop(ddlHost{e: e, op: s.op}, auth, stmt)
	}
	e.mu.Unlock()
	if err != nil {
		return ddl.SchemaDropManifest{}, err
	}
	var refScratch [8]ids.ObjectRef
	refs := refScratch[:0]
	for _, target := range manifest.Targets {
		if target.Ref != (ids.ObjectRef{}) {
			refs = append(refs, target.Ref)
		}
	}
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	refs = slices.Compact(refs)
	for _, ref := range refs {
		if err := s.acquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
			return ddl.SchemaDropManifest{}, err
		}
	}
	// Validation and identity-bound construction belong to one subsequent
	// Engine.mu section. Do not validate here and discard the captured names.
	return manifest, nil
}

// A conditional creation may skip only its original destination, never an
// unrelated companion collision. Recheck current namespace authority after
// waiting; do not rerun the command or adopt the peer's created identity.
func (s *StatementScope) skipConcurrentRelation(base catalog.Read, key ids.NameKey, name parser.ObjectName, failure error) (bool, error) {
	var diagnostic *sqlstate.Error
	if !errors.As(failure, &diagnostic) || diagnostic.Code != "23505" {
		return false, failure
	}
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	exists := err == nil && auth.Catalog.NameAddress(key) != (ids.ObjectAddress{})
	if exists {
		if auth.Catalog.Epoch() != base.Epoch() || auth.Catalog.Get(key.Namespace) == nil {
			err = ids.ErrCatalogReplaced
		} else {
			resolved, resolveErr := e.creationNamespace(auth, name)
			if resolveErr != nil {
				err = ddl.CreationError(resolveErr, name)
			} else if resolved.Address != key.Namespace {
				// A different current search-path destination cannot authorize
				// skipping the originally captured binding.
				exists = false
			} else {
				err = ddl.CreationError(auth.CheckCreate(resolved), name)
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return false, err
	}
	if !exists {
		return false, failure
	}
	s.op.ExistingRelationNotice(key.Name)
	return true, nil
}

func (s *StatementScope) skipConcurrentSchema(stage *CatalogStage, stmt *parser.CreateSchema, failure error) (bool, error) {
	var diagnostic *sqlstate.Error
	if !errors.As(failure, &diagnostic) || diagnostic.Code != "23505" {
		return false, failure
	}
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	var name string
	if err == nil {
		_, name, err = ddl.ResolveSchemaCreation(ddlHost{e: e, op: s.op}, auth, stmt)
	}
	exists := err == nil && auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name}) != (ids.ObjectAddress{})
	if exists && auth.Catalog.Epoch() != stage.Base.Epoch() {
		err = ids.ErrCatalogReplaced
	}
	e.mu.Unlock()
	if err != nil {
		return false, err
	}
	if !exists {
		return false, failure
	}
	s.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P06", Message: fmt.Sprintf(`schema "%s" already exists, skipping`, name)})
	return true, nil
}
