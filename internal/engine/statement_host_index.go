package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// The write host's windows take Engine.mu themselves and never wait; its
// waits run with the caller holding nothing (index.WriteHost). The probe,
// claim and exclusion loops call the concrete methods, never the interface.
var _ index.WriteHost = (*statementHost)(nil)

// writeHost is the statement's index write host.
func (s *StatementScope) writeHost() *statementHost { return (*statementHost)(s) }

// Admission owns private overlays for this statement; committed immutable roots
// are recaptured only after each key grant, including after peer outcome waits.
func (h *statementHost) UniqueParent(ref ids.ObjectRef) (*catalog.Relation, catalog.Read, error) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return nil, catalog.Read{}, ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(s.owner.Catalog)
	parent, ok := view.Relation(ref.Address)
	if !ok {
		return nil, catalog.Read{}, errors.New("stuntgres: invalid unique parent")
	}
	return parent, view, nil
}

func (h *statementHost) BindingAuthority(b *index.UniqueWriteBinding, view catalog.Read) (bool, error) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	if e.privateCatalogLocked(s.owner.Catalog).State() != view.State() {
		e.mu.Unlock()
		return true, nil
	}
	auth, captureErr := s.writeAuthorizationLocked(e)
	if captureErr == nil {
		auth.Catalog = view
		auth.Recapture(view)
		b.TypeName, captureErr = e.captureTypeDiagnosticName(auth)
	}
	e.mu.Unlock()
	if captureErr != nil {
		return false, captureErr
	}
	return false, s.readmitPreparedPrograms(auth)
}

func (h *statementHost) ClaimUniqueKey(ref ids.ObjectRef, bound *index.UniqueBoundKey, key []sqltype.Value) error {
	s := h.scope()
	e := s.engine()
	resource, unpin, err := e.pinUniqueKey(s.op.Context(), s.ticket, ref, bound.Ordering, bound.Grouping, key)
	if err != nil {
		return err
	}
	defer unpin()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, resource, lock.UniqueClaim, policy)
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
	if request != nil {
		return s.readmitProgramsAfterWait()
	}
	return nil
}

func (h *statementHost) WaitTransaction(owner *lock.Ticket, purpose lock.CompletionPurpose) error {
	s := h.scope()
	e := s.engine()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext, CompletionPurpose: purpose}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.CompletionOf(owner), lock.TransactionCompletion, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err == nil && request != nil {
		return s.readmitProgramsAfterWait()
	}
	return err
}

// CaptureUniqueProbe is captureUniqueProbe without the write authority.
func (h *statementHost) CaptureUniqueProbe(ref ids.ObjectRef, b *index.UniqueWriteBinding, idx *catalog.Relation, heads bool) (index.UniqueProbe, error) {
	probe, _, err := h.captureUniqueProbe(ref, b, idx, heads)
	return probe, err
}

// captureUniqueProbe also returns the write authority captured in the same
// window, for the ON CONFLICT check and the conflict's disclosure.
func (h *statementHost) captureUniqueProbe(ref ids.ObjectRef, b *index.UniqueWriteBinding, idx *catalog.Relation, heads bool) (index.UniqueProbe, AuthorizationView, error) {
	s := h.scope()
	e := s.engine()
	parent := b.Parent
	e.mu.Lock()
	if ref.Epoch != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return index.UniqueProbe{}, AuthorizationView{}, ids.ErrCatalogReplaced
	}
	current := e.privateCatalogLocked(s.owner.Catalog)
	if current.State() != b.Catalog {
		e.mu.Unlock()
		return index.UniqueProbe{Stale: true}, AuthorizationView{}, nil
	}
	currentParent, parentOK := current.Relation(ref.Address)
	currentIndex, indexOK := current.Relation(idx.Address())
	if !parentOK || !indexOK || currentParent.Revision() != parent.Revision() || !index.SameRelationRowShape(parent, currentParent) || currentIndex.Revision() != idx.Revision() || !index.SameIndexKeySemantics(idx, currentIndex) {
		e.mu.Unlock()
		return index.UniqueProbe{}, AuthorizationView{}, ids.ErrCatalogReplaced
	}
	parent, idx = currentParent, currentIndex
	auth, err := s.writeAuthorizationLocked(e)
	auth.Catalog = current
	auth.Recapture(auth.Catalog)
	namespace, _ := current.Namespace(parent.Namespace())
	root := e.storage.IndexRoots[idx.Address()]
	if changes := s.owner.Catalog; changes != nil {
		if private := index.JournalIndexesOrZero(changes).UniqueRoots[idx.Address()]; private != nil {
			root = private
		}
	}
	var versions []storage.RowVersion
	if heads {
		if stored := e.privateStorageLocked(s.owner.Catalog, ref.Address); stored != nil {
			versions = stored.Versions
		}
	}
	e.mu.Unlock()
	if err != nil {
		return index.UniqueProbe{}, AuthorizationView{}, err
	}
	if namespace == nil {
		return index.UniqueProbe{}, AuthorizationView{}, ids.ErrCatalogReplaced
	}
	return index.UniqueProbe{Parent: parent, Index: idx, View: current, Schema: namespace.Name(), Root: root, Versions: versions}, auth, nil
}

func (h *statementHost) ExistingIndexRoot(idx *catalog.Relation) (*storage.IndexRoot, error) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	current := e.privateCatalogLocked(s.owner.Catalog)
	currentIndex, ok := current.Relation(idx.Address())
	if !ok || !index.SameIndexKeySemantics(idx, currentIndex) {
		return nil, ids.ErrCatalogReplaced
	}
	root := e.storage.IndexRoots[idx.Address()]
	if changes := s.owner.Catalog; changes != nil && index.JournalIndexesOrZero(changes).UniqueRoots[idx.Address()] != nil {
		root = index.JournalIndexesOrZero(changes).UniqueRoots[idx.Address()]
	}
	return root, nil
}

// CaptureExclusionScan is captureExclusionScan without the write authority.
func (h *statementHost) CaptureExclusionScan(ref ids.ObjectRef, b *index.UniqueWriteBinding, idx *catalog.Relation) (index.ExclusionScan, error) {
	scan, _, err := h.captureExclusionScan(ref, b, idx)
	return scan, err
}

// captureExclusionScan also returns the write authority captured in the same
// window, for the conflict's disclosure.
func (h *statementHost) captureExclusionScan(ref ids.ObjectRef, b *index.UniqueWriteBinding, idx *catalog.Relation) (index.ExclusionScan, AuthorizationView, error) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	view := e.privateCatalogLocked(s.owner.Catalog)
	if ref.Epoch != e.catalog.state.Epoch() || view.State() != b.Catalog {
		return index.ExclusionScan{}, AuthorizationView{}, ids.ErrCatalogReplaced
	}
	auth, err := s.writeAuthorizationLocked(e)
	if err != nil {
		return index.ExclusionScan{}, AuthorizationView{}, err
	}
	if admission := s.foreignActionsOrZero().admissions[ref.Address]; admission != nil && admission.mutated {
		auth = foreignActionOwner(auth, admission.relation.Owner)
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	root := e.storage.IndexRoots[idx.Address()]
	if s.owner.Catalog != nil {
		if private, ok := index.JournalIndexesOrZero(s.owner.Catalog).UniqueRoots[idx.Address()]; ok {
			root = private
		}
	}
	ns, ok := view.Namespace(b.Parent.Namespace())
	if !ok {
		return index.ExclusionScan{}, AuthorizationView{}, fmt.Errorf("stuntgres: missing exclusion namespace")
	}
	store := e.privateStorageLocked(s.owner.Catalog, b.Parent.Address())
	if store == nil {
		return index.ExclusionScan{}, AuthorizationView{}, fmt.Errorf("stuntgres: missing exclusion row storage")
	}
	generation, pending := e.exclusions.ScanLocked()
	return index.ExclusionScan{Generation: generation, Root: root, Pending: pending, View: view, Schema: ns.Name(), Parent: b.Parent.Address(), Store: store, StoreGeneration: store.Generation, Versions: store.Versions, Ends: store.Ends}, auth, nil
}

func (h *statementHost) ExclusionScanCurrent(scan index.ExclusionScan, idx ids.ObjectAddress) bool {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	root := e.storage.IndexRoots[idx]
	if s.owner.Catalog != nil {
		if private, ok := index.JournalIndexesOrZero(s.owner.Catalog).UniqueRoots[idx]; ok {
			root = private
		}
	}
	store := e.privateStorageLocked(s.owner.Catalog, scan.Parent)
	return e.exclusions.CurrentLocked(scan.Generation) && scan.View.State() == e.privateCatalogLocked(s.owner.Catalog).State() && scan.Root == root && store == scan.Store && store.Generation == scan.StoreGeneration && len(store.Versions) == len(scan.Versions) && store.Ends == scan.Ends
}

func (h *statementHost) PublishExclusion(ref ids.ObjectRef, state *catalog.State, proposal *index.WriteCandidate, edit index.ExclusionEdit) (storage.RowVersion, error) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Epoch != e.catalog.state.Epoch() || e.privateCatalogLocked(s.owner.Catalog).State() != state {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	s.beginExclusionProjectionLocked(e)
	row, err := s.exclusionCandidateLocked(e, proposal, edit.Excluded, state)
	if err != nil {
		return storage.RowVersion{}, err
	}
	row.IndexOutputs = proposal.Indexes
	if edit.Precheck {
		row.IndexOutputs = proposal.Precheck
		proposal.SharedPrecheck = true
	} else {
		proposal.SharedIndexes = true
	}
	proposal.Exclusion = &row
	p := e.exclusions.ProjectionLocked(s.ticket)
	entries := p.Entries[edit.Index]
	next := make([]index.ExclusionEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.Row.Row != row.Row {
			next = append(next, entry)
		}
	}
	if edit.Member {
		next = append(next, index.ExclusionEntry{Row: row, Keys: edit.Keys, Admitted: edit.Admitted})
	}
	// A later index insertion preserves the projections of earlier indexes.
	p.Entries[edit.Index] = next
	if len(edit.Excluded) != 0 {
		ends := maps.Clone(p.Ended[ref.Address])
		if ends == nil {
			ends = make(map[ids.RowID]ids.VersionID)
		}
		for _, old := range edit.Excluded {
			ends[old.Row] = old.Version
			if old.Base != 0 {
				ends[old.Row] = old.Base
			}
		}
		p.Ended[ref.Address] = ends
	}
	s.installExclusionProjectionLocked(e, p)
	return row, nil
}

func (h *statementHost) DiscardExclusion(proposal *index.WriteCandidate) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if proposal.Exclusion == nil {
		return
	}
	version := proposal.Exclusion.Version
	proposal.Exclusion = nil
	if version == 0 {
		return
	}
	p := e.exclusions.ProjectionLocked(s.ticket)
	for idx, entries := range p.Entries {
		if !slices.ContainsFunc(entries, func(e index.ExclusionEntry) bool { return e.Row.Version == version }) {
			continue
		}
		next := make([]index.ExclusionEntry, 0, len(entries)-1)
		for _, entry := range entries {
			if entry.Row.Version != version {
				next = append(next, entry)
			}
		}
		p.Entries[idx] = next
	}
	s.installExclusionProjectionLocked(e, p)
}

func (h *statementHost) OwnExclusionKeys(idx ids.ObjectAddress, version ids.VersionID) []sqltype.Value {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	projection := e.exclusions.OwnLocked(s.ticket)
	if projection == nil {
		return nil
	}
	for _, entry := range projection.Entries[idx] {
		if entry.Row.Version == version {
			return entry.Keys
		}
	}
	return nil
}

func (h *statementHost) AdmitExclusion(idx ids.ObjectAddress, version ids.VersionID) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	projected := e.exclusions.ProjectionLocked(s.ticket)
	entries := slices.Clone(projected.Entries[idx])
	for at := range entries {
		if entries[at].Row.Version == version {
			entries[at].Admitted = true
		}
	}
	projected.Entries[idx] = entries
	s.installExclusionProjectionLocked(e, projected)
}
