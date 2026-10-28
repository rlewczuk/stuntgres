package core

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// This is the ordinary ticket/request lifecycle, including savepoint checkpoints
// and replacement fencing. No manager call or wait is made under Engine.mu.
func (s *StatementScope) acquireTransactionResource(resource lock.Resource, mode lock.Mode) error {
	e := s.Engine()
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, resource, mode, policy)
	if request != nil {
		s.admissionWaited = true
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	if s.implicit && !s.ReplacementRead {
		e.ReplacementMu.RLock()
		s.ReplacementRead = true
	}
	return nil
}

func (s *StatementScope) AcquireCatalogResource(resource lock.Resource, mode lock.Mode) error {
	// These unique-index claims project a real blocker transaction, including
	// the interval before an implicit statement reaches catalog publication.
	if resource.Name.ClassID == ids.ParameterClassID || resource.Name.ClassID == ids.SubscriptionClassID || resource.Name.ClassID == ids.StatisticExtClassID || resource.Name.ClassID == ids.CollationClassID || resource.Name.ClassID == ids.ConversionClassID || resource.Name.ClassID == ids.CastClassID {
		e := s.Engine()
		e.Mu.Lock()
		err := e.AssignTransactionXIDLocked(s.TxRef)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
	}
	claim := resource
	if resource.Address.ClassID == ids.ParameterClassID || resource.Name.ClassID == ids.ParameterClassID || resource.Address.ClassID == ids.SubscriptionClassID || resource.Name.ClassID == ids.SubscriptionClassID {
		claim.ObjectRef = s.Engine().Cluster.RoleDomain.Ref(1, resource.Address)
	}
	if err := s.acquireTransactionResource(claim, mode); err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if resource.Domain() != e.Catalog.Domain || resource.Epoch != s.TxRef.Epoch() || resource.Epoch != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return nil
}

// Claim only changed destinations, not reads or vacated names. Acquisition is
// in pure class/namespace/name/signature order. Diagnostic precedence is selected only
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
		definition := key.ClassID == ids.AccessMethodClassID || key.ClassID == ids.OperatorFamilyClassID || key.ClassID == ids.OperatorClassCatalogID || key.ClassID == ids.AccessMethodOperatorClassID || key.ClassID == ids.AccessMethodProcedureClassID
		supported := definition || key.ClassID == ids.SubscriptionClassID || key.ClassID == ids.StatisticExtClassID || key.ClassID == ids.NamespaceClassID || key.ClassID == ids.RelationClassID || key.ClassID == ids.TypeClassID || key.ClassID == ids.ProcedureClassID || key.ClassID == ids.OperatorClassID || key.ClassID == ids.CollationClassID || key.ClassID == ids.ConversionClassID || key.ClassID == ids.CastClassID
		if !supported || stage.Base.NameAddress(key) == address {
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
		if collation, ok := stage.Get(address).(*catalog.Collation); ok {
			claim.CollationEncoding = collation.Encoding
		}
		if typ, ok := stage.Get(address).(*catalog.Type); ok && stage.Base.Get(address) != nil && typ.OwningRelation != (ids.ObjectAddress{}) {
			claim.RelationRename = true
		}
		claims = append(claims, claim)
		if key.Namespace.ClassID == ids.NamespaceClassID && stage.Base.Get(key.Namespace) != nil {
			namespaces = append(namespaces, key.Namespace)
		}
	}
	if len(claims) == 0 {
		return nil
	}
	slices.SortFunc(namespaces, func(a, b ids.ObjectAddress) int { return cmp.Compare(a.ObjectID, b.ObjectID) })
	namespaces = slices.Compact(namespaces)
	for _, address := range namespaces {
		if err := s.AcquireCatalogNamespace(stage.Base, address, stage.CreationName); err != nil {
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
		if n := cmp.Compare(a.Key.Name, b.Key.Name); n != 0 {
			return n
		}
		return cmp.Compare(a.Key.Signature, b.Key.Signature)
	})
	for _, claim := range claims {
		resource := lock.Resource{ObjectRef: stage.Owner.Catalog.Domain.Ref(stage.Base.Epoch(), claim.Key.Namespace), Name: claim.Key}
		if err := s.AcquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
	}
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
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
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	for _, claim := range claims {
		if claim.Key.ClassID == ids.RelationClassID && stage.Base.Get(claim.Address) == nil {
			ref := stage.Owner.Catalog.Domain.Ref(stage.Base.Epoch(), claim.Address)
			if err := s.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
				return err
			}
		}
	}
	stage.namesAdmitted = true
	return nil
}

// deferAlterCatalogNames retains only genuine changed destinations and their
// original epoch. New relation identities still receive ordinary admission;
// only prospective logical names are deferred, never object ownership.
func (s *StatementScope) deferAlterCatalogNames(stage *CatalogStage) error {
	var namespaces []ids.ObjectAddress
	for key, address := range stage.Names() {
		if address == (ids.ObjectAddress{}) || stage.Base.NameAddress(key) == address {
			continue
		}
		if s.alterNames == nil {
			s.alterNames = make(map[ids.NameKey]ids.ObjectRef)
		}
		if _, exists := s.alterNames[key]; !exists {
			s.alterNames[key] = stage.Owner.Catalog.Domain.Ref(stage.Base.Epoch(), key.Namespace)
		}
		if key.Namespace.ClassID == ids.NamespaceClassID && stage.Base.Get(key.Namespace) != nil {
			namespaces = append(namespaces, key.Namespace)
		}
	}
	slices.SortFunc(namespaces, func(a, b ids.ObjectAddress) int { return cmp.Compare(a.ObjectID, b.ObjectID) })
	for _, address := range slices.Compact(namespaces) {
		if err := s.AcquireCatalogNamespace(stage.Base, address, stage.CreationName); err != nil {
			return err
		}
	}
	for key, address := range stage.Names() {
		if key.ClassID != ids.RelationClassID || address == (ids.ObjectAddress{}) || stage.Base.NameAddress(key) == address || stage.Base.Get(address) != nil {
			continue
		}
		ref := stage.Owner.Catalog.Domain.Ref(stage.Base.Epoch(), address)
		if err := s.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) acquireAlterCatalogNames() error {
	if len(s.alterNames) == 0 {
		return nil
	}
	keys := make([]ids.NameKey, 0, len(s.alterNames))
	for key := range s.alterNames {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b ids.NameKey) int {
		if n := cmp.Compare(a.ClassID, b.ClassID); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Namespace.ObjectID, b.Namespace.ObjectID); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return cmp.Compare(a.Signature, b.Signature)
	})
	for _, key := range keys {
		resource := lock.Resource{ObjectRef: s.alterNames[key], Name: key}
		if err := s.AcquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) AcquireCatalogNamespace(base catalog.Read, address ids.ObjectAddress, name parser.ObjectName) error {
	ref := s.Engine().Catalog.Domain.Ref(base.Epoch(), address)
	if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil && auth.Catalog.Get(address) == nil {
		err = ddl.CreationError(SQLError("3F000", fmt.Sprintf(`schema "%s" does not exist`, base.Get(address).Name())), name)
	}
	return err
}

// A conditional creation may skip only its original destination, never an
// unrelated companion collision. Recheck current namespace authority after
// waiting; do not rerun the command or adopt the peer's created identity.
func (s *StatementScope) SkipConcurrentRelation(base catalog.Read, key ids.NameKey, name parser.ObjectName, failure error) (bool, error) {
	var diagnostic *sqlstate.Error
	if !errors.As(failure, &diagnostic) || diagnostic.Code != "23505" {
		return false, failure
	}
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	exists := err == nil && auth.Catalog.NameAddress(key) != (ids.ObjectAddress{})
	if exists {
		if auth.Catalog.Epoch() != base.Epoch() || auth.Catalog.Get(key.Namespace) == nil {
			err = ids.ErrCatalogReplaced
		} else {
			resolved, resolveErr := e.CreationNamespace(auth, name)
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
	e.Mu.Unlock()
	if err != nil {
		return false, err
	}
	if !exists {
		return false, failure
	}
	s.Op.ExistingRelationNotice(key.Name)
	return true, nil
}
