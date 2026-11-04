package core

import (
	"fmt"
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
)

type selectedDropRoot struct {
	reference ids.ObjectRef
	namespace bool
	original  catalog.Descriptor
}

type dropCleanupOrigin uint8

const (
	dropCleanupSQL dropCleanupOrigin = iota
	dropCleanupOnCommit
	dropCleanupDrained
)

// SelectDropRoot requires Engine.Mu. It selects a genuine SQL DROP root without
// deleting or projecting anything. Ordinary Remove/RemoveObject retain their
// physical semantics; no reader is told this still-live root has disappeared.
func (s *CatalogStage) SelectDropRoot(ref ids.ObjectRef, namespace bool) error {
	if s == nil || s.Owner == nil || s.Lifecycle != CatalogOpen || s.Invalid != nil || s.selectedDropsApplied || ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid DROP root selection")
	}
	if err := s.Owner.Catalog.ValidateObjectRefLocked(s.Base, ref); err != nil {
		return err
	}
	if err := table.CheckRemovalLocked(s.Base, s.DependencyPath, ref, namespace); err != nil {
		return err
	}
	for _, selected := range s.selectedDropRoots {
		if selected.reference == ref {
			if selected.namespace != namespace {
				return ids.ErrCatalogReplaced
			}
			return nil
		}
	}
	s.selectedDropRoots = append(s.selectedDropRoots, selectedDropRoot{ref, namespace, s.Base.Get(ref.Address)})
	return nil
}

func (s *CatalogStage) SelectedDropRoots() iter.Seq[ids.ObjectRef] {
	return func(yield func(ids.ObjectRef) bool) {
		for _, selected := range s.selectedDropRoots {
			if !yield(selected.reference) {
				return
			}
		}
	}
}

// ApplySelectedDropRoots follows the selector's original physical order, only
// after the actual controller has initialized its complete captured source
// cohort. Overlapping roots retain normal owned-removal lineage/idempotence.
func (s *CatalogStage) ApplySelectedDropRoots(plan *DropDependencyPlan) error {
	if s == nil || s.Owner == nil || s.Lifecycle != CatalogOpen || s.Invalid != nil || s.selectedDropsApplied || plan == nil || plan.stage != s || plan.provider.Stage != s || plan.consumed || plan.selected != len(s.selectedDropRoots) {
		return fmt.Errorf("stuntgres: invalid DROP root replay")
	}
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	s.selectedDropsApplied = true
	for _, selected := range s.selectedDropRoots {
		if s.Base.Get(selected.reference.Address) != selected.original {
			return ids.ErrCatalogReplaced
		}
		if err := s.RemoveObjectLocked(selected.reference, selected.namespace); err != nil {
			return err
		}
	}
	return nil
}

func (p *DropProvider) selectedDropRootUnion(roots []ids.ObjectAddress) ([]ids.ObjectAddress, int) {
	if p.Stage == nil || len(p.Stage.selectedDropRoots) == 0 {
		return roots, 0
	}
	count := len(p.Stage.selectedDropRoots)
	seen := make(map[ids.ObjectAddress]bool, len(roots)+count)
	for _, address := range roots {
		seen[address] = true
	}
	original := true
	for _, selected := range p.Stage.selectedDropRoots {
		address := selected.reference.Address
		if seen[address] {
			continue
		}
		if original {
			union := make([]ids.ObjectAddress, len(roots), len(roots)+count)
			copy(union, roots)
			roots = union
			original = false
		}
		roots = append(roots, address)
		seen[address] = true
	}
	return roots, count
}
