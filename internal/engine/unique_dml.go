package engine

import (
	"errors"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) bindUniqueWrite(ref ids.ObjectRef) (*index.UniqueWriteBinding, error) {
retry:
	parent, view, err := s.writeHost().UniqueParent(ref)
	if err != nil {
		return nil, err
	}
	if b := s.uniqueBinding; b != nil && b.Ref == ref && b.Parent == parent && b.Catalog == view.State() {
		return b, nil
	}
	if b := s.indexBindings[ref.Address]; b != nil && b.Ref == ref && b.Parent == parent && b.Catalog == view.State() {
		s.uniqueBinding = b
		return b, nil
	}
	if overlay := s.writeOverlay(); overlay != nil && s.uniqueBinding != nil && s.uniqueBinding.Ref == ref {
		overlay.overlays = nil
	}
	b := &index.UniqueWriteBinding{Ref: ref, Parent: parent, Catalog: view.State()}
	var indexes []*catalog.Relation
	var checkedNonunique []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Parent != parent.Address() || !idx.SQLIndex.Live {
			continue
		}
		if idx.SQLIndex.Ready && idx.SQLIndex.RequiresInsertionOutput() {
			b.Checked = true
			if !idx.SQLIndex.Unique && !idx.SQLIndex.IsExclusion() {
				checkedNonunique = append(checkedNonunique, idx)
			}
		}
		if !idx.SQLIndex.Unique && !idx.SQLIndex.IsExclusion() {
			continue
		}
		// REL18 row-key eligibility is independent of readiness/validity.
		// An expression anywhere excludes the whole index; INCLUDE is payload.
		var slots [32]int
		if len(idx.SQLIndex.Keys) > len(slots) {
			return nil, errors.New("stuntgres: invalid unique index key count")
		}
		simple := true
		for at, key := range idx.SQLIndex.Keys {
			attribute, node, err := index.CatalogKey(parent, key)
			if err != nil {
				return nil, err
			}
			if node != nil {
				simple = false
				break
			}
			slots[at] = slices.Index(parent.Attributes, attribute)
		}
		if idx.SQLIndex.Unique && simple && idx.SQLIndex.Predicate == nil {
			for _, slot := range slots[:len(idx.SQLIndex.Keys)] {
				if !slices.Contains(b.KeySlots, slot) {
					b.KeySlots = append(b.KeySlots, slot)
				}
			}
		}
		if idx.SQLIndex.Ready {
			indexes = append(indexes, idx)
		}
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	b.Keys = make([]index.UniqueBoundKey, len(indexes))
	for i, idx := range indexes {
		bound := &b.Keys[i]
		bound.Index = idx
		bound.Parent = parent
		if idx.SQLIndex.IsExclusion() {
			bound.Ordering, err = index.BindExclusionOrdering(parent, idx.SQLIndex)
		} else {
			bound.Ordering, err = index.BindIndexOrdering(parent, idx.SQLIndex)
		}
		if err != nil {
			return nil, err
		}
		n := len(idx.SQLIndex.Keys)
		if idx.SQLIndex.Method != catalog.GistMethod {
			bound.Grouping = make([]sqltype.Grouping, n)
		}
		bound.Key, bound.Scratch = make([]sqltype.Value, n), make([]sqltype.Value, n)
		for j, key := range idx.SQLIndex.Keys {
			usage, usageErr := index.KeyUsage(parent, key)
			if usageErr != nil {
				return nil, usageErr
			}
			if idx.SQLIndex.Method == catalog.GistMethod {
				continue
			}
			// network_ops owns typed comparison but not generic grouping. Its
			// claims use the existing comparator-based shared bucket.
			if key.Class.Family == "network_ops" {
				continue
			}
			bound.Grouping[j], err = sqltype.BindGrouping(usage)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(checkedNonunique) != 0 {
		b.Insertion = make([]index.InsertionBinding, 0, len(b.Keys)+len(checkedNonunique))
		for i := range b.Keys {
			key := &b.Keys[i]
			b.Insertion = append(b.Insertion, index.InsertionBinding{Index: key.Index, Unique: key})
		}
		for _, idx := range checkedNonunique {
			bound := index.InsertionBinding{Index: idx, Parent: parent}
			if idx.SQLIndex.Method == catalog.HashMethod {
				bound.Hash, err = index.BindHashIndex(parent, idx.SQLIndex)
			} else {
				bound.Ordering, err = index.BindIndexKeys(parent, idx.SQLIndex)
			}
			if err != nil {
				return nil, err
			}
			b.Insertion = append(b.Insertion, bound)
		}
		slices.SortFunc(b.Insertion, func(a, b index.InsertionBinding) int {
			if a.Index.Address().ObjectID < b.Index.Address().ObjectID {
				return -1
			}
			if a.Index.Address().ObjectID > b.Index.Address().ObjectID {
				return 1
			}
			return 0
		})
	}
	if err := b.InheritConsumedPrograms(s.indexBindings[ref.Address]); err != nil {
		return nil, err
	}
	if b.Checked {
		stale, err := s.writeHost().BindingAuthority(b, view)
		if err != nil {
			return nil, err
		}
		if stale {
			goto retry
		}
	}
	s.uniqueBinding = b
	if b.Checked {
		if s.indexBindings == nil {
			s.indexBindings = make(map[ids.ObjectAddress]*index.UniqueWriteBinding)
		}
		s.indexBindings[ref.Address] = b
	}
	return b, nil
}

func (s *StatementScope) claimUniqueRow(ref ids.ObjectRef, row storage.RowVersion) error {
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Input: s.input, Output: s.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.Catalog = s.engine().catalog.read(binding.Catalog)
	if binding.TypeName != nil {
		evaluation.Input.TypeDiagnosticName = binding.TypeName
	}
	purpose := index.ProbeRuntime
	if binding.Checked {
		purpose = index.ProbeTracked
	}
	for i := range binding.Keys {
		bound := &binding.Keys[i]
		idx := bound.Index
		if idx.SQLIndex.IsExclusion() {
			c := index.ExclusionDefinition(binding.Parent, idx)
			if c == nil {
				return ids.ErrCatalogReplaced
			}
			if c.Deferrable || !idx.SQLIndex.ExactEqualityClaims() {
				continue
			}
		}
		member, key, err := s.existingUniqueOutput(bound, row, &evaluation, purpose)
		if err != nil {
			return err
		}
		if !member {
			continue
		}
		if !idx.SQLIndex.NullsNotDistinct && index.UniqueHasNull(key) {
			continue
		}
		indexRef := ref
		indexRef.Address = idx.Address()
		if err := s.writeHost().ClaimUniqueKey(indexRef, bound, key); err != nil {
			return err
		}
	}
	return nil
}

// One real provider probe supplies ordinary violations, omission visibility and
// exact conflict-update identity without scanning committed table payloads.
type uniqueConflict struct {
	parent, index *catalog.Relation
	key           []sqltype.Value
	existing      []sqltype.Value
	auth          AuthorizationView
	view          catalog.Read
	schema        string
	row           storage.RowVersion
	committed     bool
	position      int
}

func (c *uniqueConflict) diagnostic(s *StatementScope, ref ids.ObjectRef) error {
	allowed, err := index.UniqueKeyDetailAllowed(c.auth.View, c.auth, ref, c.index.SQLIndex.Keys)
	if err != nil {
		return err
	}
	if c.index.SQLIndex.IsExclusion() {
		return index.ExclusionDiagnostic(s.op.Context(), c.parent, c.index, c.key, c.existing, c.schema, s.output, allowed, false, c.view)
	}
	return index.UniqueDiagnostic(s.op.Context(), c.parent, c.index, c.key, c.schema, s.output, allowed, false, c.view)
}
func (s *StatementScope) checkUniqueRow(ref ids.ObjectRef, candidate *index.WriteCandidate, excluded []storage.RowVersion, prefix index.WriteBatch) error {
	conflict, err := s.probeUniqueRow(ref, candidate, excluded, prefix, nil, index.ProbeRuntime)
	if err != nil {
		return err
	}
	if conflict.index != nil {
		return conflict.diagnostic(s, ref)
	}
	return nil
}
func (s *StatementScope) probeUniqueRow(ref ids.ObjectRef, candidate *index.WriteCandidate, excluded []storage.RowVersion, prefix index.WriteBatch, action *insertConflictState, purpose index.ProbePurpose) (uniqueConflict, error) {
	row := candidate.Values
retry:
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return uniqueConflict{}, err
	}
	if purpose == index.ProbeRuntime && binding.Checked {
		purpose = index.ProbeTracked
	}
	parent := binding.Parent
	if len(row) != len(parent.Columns) {
		return uniqueConflict{}, errors.New("stuntgres: invalid staged row width")
	}
	e := s.engine()
	ctx := s.op.Context()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(s), Input: s.input, Output: s.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.Catalog = e.catalog.read(binding.Catalog)
	if binding.TypeName != nil {
		evaluation.Input.TypeDiagnosticName = binding.TypeName
	}
	if action != nil && action.catalog != binding.Catalog {
		view := e.catalog.read(binding.Catalog)
		if err := action.selectArbiters(&evaluation, parent, view); err != nil {
			return uniqueConflict{}, err
		}
	}
	length := len(binding.Keys)
	if action == nil && binding.Insertion != nil {
		length = len(binding.Insertion)
	}
	for i := 0; i < length; i++ {
		var bound *index.UniqueBoundKey
		if action == nil && binding.Insertion != nil {
			insertion := &binding.Insertion[i]
			if insertion.Unique == nil {
				if purpose == index.ProbeRevalidation {
					if output, exists := candidate.Indexes[insertion.Index.Address()]; !exists || !index.OutputMatches(output, insertion.Index) {
						return uniqueConflict{}, ids.ErrCatalogReplaced
					}
				} else if err := candidate.MaterializeInsertion(insertion, &evaluation); err != nil {
					return uniqueConflict{}, err
				}
				continue
			}
			bound = insertion.Unique
		} else {
			bound = &binding.Keys[i]
		}
		idx := bound.Index
		if idx.SQLIndex.IsExclusion() {
			if conflict, err := s.probeExclusionRow(ref, binding, bound, candidate, excluded, action, purpose); err != nil || conflict.index != nil {
				return conflict, err
			}
			continue
		}
		if action != nil && !slices.Contains(action.selected, idx.Address()) {
			continue
		}
		member, key, err := candidate.UniqueOutput(bound, &evaluation, action != nil, purpose)
		if err != nil {
			return uniqueConflict{}, err
		}
		if !member {
			continue
		}
		if !idx.SQLIndex.NullsNotDistinct && index.UniqueHasNull(key) {
			continue
		}
		indexRef := ref
		indexRef.Address = idx.Address()
		if err := s.writeHost().ClaimUniqueKey(indexRef, bound, key); err != nil {
			return uniqueConflict{}, err
		}
		heads := action != nil && (action.Update != nil || stableIsolation(s.owner.Isolation) || idx.SQLIndex.HasRuntimeDomainKeyConversion())
		probe, auth, err := s.writeHost().captureUniqueProbe(ref, binding, idx, heads)
		if err != nil {
			return uniqueConflict{}, err
		}
		if probe.Stale {
			s.uniqueBinding = nil
			goto retry
		}
		parent, idx = probe.Parent, probe.Index
		current, root, versions := probe.View, probe.Root, probe.Versions
		if check := s.insertConflict; check != nil {
			if err := analyze.CheckBinding(auth, bindingForConflict(ref, parent, check), &parser.Insert{}); err != nil {
				return uniqueConflict{}, err
			}
		}
		if root == nil {
			return uniqueConflict{}, errors.New("stuntgres: ready unique index root is missing")
		}
		outcome := uniqueConflict{parent: parent, index: idx, key: key, auth: auth, view: current, schema: probe.Schema}
		lo, hi := 0, len(root.Entries)
		for lo < hi {
			mid := lo + (hi-lo)/2
			cmp, err := bound.Ordering.CompareKeys(ctx, root.Entries[mid], storage.IndexEntry{Keys: key})
			if err != nil {
				return uniqueConflict{}, err
			}
			if cmp < 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		for at := lo; at < len(root.Entries); at++ {
			entry := root.Entries[at]
			cmp, err := bound.Ordering.CompareKeys(ctx, entry, storage.IndexEntry{Keys: key})
			if err != nil {
				return uniqueConflict{}, err
			}
			if cmp != 0 {
				break
			}
			if index.UniqueRowExcluded(entry.Row, excluded) || index.UniqueRowDeleted(entry.Row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
				continue
			}
			replaced := false
			if c := s.writeOverlay(); c != nil {
				_, replaced = c.affectedRows[entry.Row]
				if !replaced {
					_, replaced = s.insertOwnRow(ref, entry.Row)
				}
			} else {
				for _, own := range s.ownWrites[ref.Address] {
					if own.Row == entry.Row {
						replaced = true
						break
					}
				}
				if !replaced {
					for _, own := range s.stage.rows[ref.Address] {
						if own.Row == entry.Row {
							replaced = true
							break
						}
					}
				}
			}
			if !replaced {
				if heads {
					head, err := index.ConflictEntryVersion(versions, entry)
					if err != nil {
						return uniqueConflict{}, err
					}
					if purpose != index.ProbeRevalidation && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
						if err := bound.FillKeys(head.Values, bound.Scratch, &evaluation); err != nil {
							return uniqueConflict{}, err
						}
					}
					outcome.row = head
					outcome.position = entry.Position
					outcome.committed = head.Born != 0
				}
				return outcome, nil
			}
		}
		if c := s.writeOverlay(); c != nil {
			own, found, err := s.probeInsertWriteOverlay(ref, bound, &evaluation, key, excluded, purpose)
			if err != nil {
				return uniqueConflict{}, err
			}
			if found {
				if purpose != index.ProbeRevalidation && action != nil && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
					if err := bound.FillKeys(own.Values, bound.Scratch, &evaluation); err != nil {
						return uniqueConflict{}, err
					}
				}
				outcome.row, outcome.position = own, -1
				return outcome, nil
			}
		} else {
			for overlayIndex, overlay := range [2][]storage.RowVersion{s.stage.rows[ref.Address], s.ownWrites[ref.Address]} {
				for _, own := range overlay {
					if overlayIndex == 1 {
						if _, _, replaced := storage.RowByID(s.stage.rows[ref.Address], own.Row, -1); replaced {
							continue
						}
					}
					if index.UniqueRowExcluded(own.Row, excluded) || index.UniqueRowDeleted(own.Row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
						continue
					}
					equal, err := s.matchesExistingUnique(bound, own, &evaluation, key, purpose)
					if err != nil {
						return uniqueConflict{}, err
					}
					if equal {
						if purpose != index.ProbeRevalidation && action != nil && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
							if err := bound.FillKeys(own.Values, bound.Scratch, &evaluation); err != nil {
								return uniqueConflict{}, err
							}
						}
						outcome.row = own
						outcome.position = -1
						return outcome, nil
					}
				}
			}
		}
		for i := range prefix.Values {
			accepted := prefix.Candidate(i)
			equal, err := accepted.MatchesUnique(bound, &evaluation, key)
			if err != nil {
				return uniqueConflict{}, err
			}
			if equal {
				return outcome, nil
			}
		}
	}
	return uniqueConflict{}, nil
}

func (s *StatementScope) uniqueKeyChanged(ref ids.ObjectRef, old, next []sqltype.Value) (bool, error) {
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return false, err
	}
	for _, slot := range binding.KeySlots {
		if !sqltype.SameDatum(old[slot], next[slot]) {
			return true, nil
		}
	}
	return false, nil
}
