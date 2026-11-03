package index

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// GinMaintenanceAuthority is captured from an existing owner's effective
// catalog while its journal/store pins are held. It is not detached authority.
type GinMaintenanceAuthority struct {
	View                   catalog.Read
	Index, Parent          *catalog.Relation
	Journal                *session.Journal
	candidate              RootCandidate
	matched, sourceCurrent bool
}

// CaptureAuthority runs under the existing owner cohort and Engine.Mu. Mutable
// storage-window and source-receipt facts are captured here, never read during
// detached extraction or comparison.
func (m *GinMaintenance) CaptureAuthority(a GinMaintenanceAuthority) GinMaintenanceAuthority {
	if a.Journal != nil {
		a.candidate = JournalIndexesOrZero(a.Journal).Candidates[m.Index.Address()]
		a.matched = a.candidate.Matches(m.Store.IndexIdentity())
		a.sourceCurrent = a.candidate.routineSourceCurrent(m.View, m.Roots, m.Stores)
	}
	if m.authorities == nil {
		m.authorities = make(map[*session.Journal]GinMaintenanceAuthority)
	}
	m.authorities[a.Journal] = a
	return a
}

// SourceCurrent is called at the locked final fence, before any root swap.
func (a GinMaintenanceAuthority) SourceCurrent(view catalog.Read, roots map[ids.ObjectAddress]*storage.IndexRoot, stores map[ids.ObjectAddress]*storage.Relation) bool {
	return a.candidate.routineSourceCurrent(view, roots, stores)
}

// Authorize certifies one actual captured owner, independently of candidate
// source receipts. Presentation shape is only a prefilter: both catalogs must
// authenticate every method, callback, member, class and concrete codec.
func (m *GinMaintenance) Authorize(a GinMaintenanceAuthority) error {
	if a.View.State() == nil || a.View.Owner() != m.View.Owner() || a.View.Epoch() != m.View.Epoch() || a.Index == nil || a.Parent == nil || a.View.Get(a.Index.Address()) != a.Index || a.View.Get(a.Parent.Address()) != a.Parent || a.Index.Address() != m.Index.Address() || a.Index.SQLIndex == nil || a.Index.SQLIndex.Method != catalog.GinMethod || a.Index.SQLIndex.Parent != a.Parent.Address() {
		return ids.ErrCatalogReplaced
	}
	bindings, err := BindGinIndex(a.View, a.Parent, a.Index.SQLIndex)
	if err != nil {
		return err
	}
	base := m.Index
	before := m.TargetView
	if before.State() == nil {
		before = m.View
	}
	if m.CommittedIndex != nil {
		base, before = m.CommittedIndex, m.View
	}
	parent, ok := before.Relation(base.SQLIndex.Parent)
	if !ok || before.Get(base.Address()) != base {
		return ids.ErrCatalogReplaced
	}
	prior, err := BindGinIndex(before, parent, base.SQLIndex)
	if err != nil {
		return err
	}
	if !SameRelationRowShape(parent, a.Parent) || len(prior) != len(bindings) {
		return ids.ErrCatalogReplaced
	}
	candidate := a.candidate
	if candidate.routineSource != nil && !a.sourceCurrent {
		return ids.ErrCatalogReplaced
	}
	routineTransport := a.matched && candidate.Index == a.Index && a.sourceCurrent && ginMaintenanceHasRoutineSource(candidate, base)
	if !ginPresentationIndexSemantics(base, a.Index) && !routineTransport {
		return fmt.Errorf("stuntgres: incompatible GIN maintenance owner authority")
	}
	for i, binding := range prior {
		current, err := ginPresentationClass(before, a.View, base.SQLIndex.Keys[i].Class)
		if err != nil {
			return err
		}
		if current != a.Index.SQLIndex.Keys[i].Class || current != bindings[i].Class {
			return ids.ErrCatalogReplaced
		}
		comparison := bindings[i]
		comparison.Class = binding.Class
		if !comparison.Same(binding) {
			return fmt.Errorf("stuntgres: GIN maintenance changed token semantics")
		}
	}
	if m.bindings == nil {
		m.bindings = make(map[*catalog.Relation]ginMaintenanceBinding)
	}
	certificate := ginMaintenanceBinding{columns: bindings, target: a.Index}
	m.bindings[a.Index] = certificate
	if routineTransport {
		for receipt := candidate.routineSource; receipt != nil; receipt = receipt.previous {
			source := certificate
			source.target = receipt.source
			m.bindings[receipt.source] = source
		}
	}
	return nil
}
func (m *GinMaintenance) OutputBindings(output storage.IndexOutput, admission *catalog.State) ([]sqltype.GinBinding, *catalog.Relation, bool, error) {
	if certificate, ok := m.bindings[output.Index]; ok {
		return certificate.columns, certificate.target, true, nil
	}
	if admission == nil || output.Index == nil {
		return nil, nil, false, nil
	}
	if admission.Get(output.Index.Address()) != output.Index {
		return nil, nil, false, ids.ErrCatalogReplaced
	}
	if output.Index.SQLIndex == nil || output.Index.Address() != m.Index.Address() || m.TargetView.State() == nil {
		return nil, nil, false, ids.ErrCatalogReplaced
	}
	source := m.View.WithState(admission)
	parent, ok := source.Relation(output.Index.SQLIndex.Parent)
	targetParent, targetOK := m.TargetView.Relation(m.Index.SQLIndex.Parent)
	if !ok || !targetOK || parent.TableMethod != targetParent.TableMethod || parent.EffectiveStorageLocator(source) != targetParent.EffectiveStorageLocator(m.TargetView) {
		return nil, nil, false, ids.ErrCatalogReplaced
	}
	current, ok := m.bindings[m.Index]
	if !ok {
		return nil, nil, false, ids.ErrCatalogReplaced
	}
	// This is the actual accepted key catalog, never old ACL authority. Both
	// catalogs bind strictly; the presentation certificate is symmetric.
	if err := m.Authorize(GinMaintenanceAuthority{View: source, Index: output.Index, Parent: parent}); err != nil {
		return nil, nil, false, err
	}
	m.bindings[output.Index] = current
	return current.columns, current.target, true, nil
}

func ginMaintenanceHasRoutineSource(candidate RootCandidate, source *catalog.Relation) bool {
	for receipt := candidate.routineSource; receipt != nil; receipt = receipt.previous {
		if receipt.source == source {
			return true
		}
	}
	return false
}
