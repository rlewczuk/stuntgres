package engine

import (
	"errors"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func bindUniqueWrite(h dml.Host, ref ids.ObjectRef) (*index.UniqueWriteBinding, error) {
	c := h.Command()
retry:
	parent, view, err := h.UniqueParent(ref)
	if err != nil {
		return nil, err
	}
	if b := c.UniqueBinding; b != nil && b.Ref == ref && b.Parent == parent && b.Catalog == view.State() {
		return b, nil
	}
	if b := c.IndexBindings[ref.Address]; b != nil && b.Ref == ref && b.Parent == parent && b.Catalog == view.State() {
		c.UniqueBinding = b
		return b, nil
	}
	if overlay := c.WriteOverlay(); overlay != nil && c.UniqueBinding != nil && c.UniqueBinding.Ref == ref {
		overlay.Overlays = nil
	}
	b, err := index.BuildUniqueWriteBinding(ref, parent, view)
	if err != nil {
		return nil, err
	}
	if err := b.InheritConsumedPrograms(c.IndexBindings[ref.Address]); err != nil {
		return nil, err
	}
	if b.Checked {
		stale, err := h.BindingAuthority(b, view)
		if err != nil {
			return nil, err
		}
		if stale {
			goto retry
		}
	}
	c.UniqueBinding = b
	if b.Checked {
		if c.IndexBindings == nil {
			c.IndexBindings = make(map[ids.ObjectAddress]*index.UniqueWriteBinding)
		}
		c.IndexBindings[ref.Address] = b
	}
	return b, nil
}

// bindingEvaluation is the evaluation context of b's key programs: the
// statement's context and environments over b's catalog.
func bindingEvaluation(h dml.Host, b *index.UniqueWriteBinding) exec.Context {
	input, output := h.Environments()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: h.Context(), Host: h, Input: input, Output: output, Catalog: h.CatalogRead(b.Catalog)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	if b.TypeName != nil {
		evaluation.Input.TypeDiagnosticName = b.TypeName
	}
	return evaluation
}

func claimUniqueRow(h dml.Host, ref ids.ObjectRef, row storage.RowVersion) error {
	binding, err := bindUniqueWrite(h, ref)
	if err != nil {
		return err
	}
	evaluation := bindingEvaluation(h, binding)
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
		member, key, err := existingUniqueOutput(h, bound, row, &evaluation, purpose)
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
		if err := h.ClaimUniqueKey(indexRef, bound, key); err != nil {
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
	auth          access.View
	view          catalog.Read
	schema        string
	row           storage.RowVersion
	committed     bool
	position      int
}

func (c *uniqueConflict) diagnostic(h dml.Host, ref ids.ObjectRef) error {
	allowed, err := index.UniqueKeyDetailAllowed(c.auth, h.DisclosureEnv(c.auth), ref, c.index.SQLIndex.Keys)
	if err != nil {
		return err
	}
	_, output := h.Environments()
	if c.index.SQLIndex.IsExclusion() {
		return index.ExclusionDiagnostic(h.Context(), c.parent, c.index, c.key, c.existing, c.schema, output, allowed, false, c.view)
	}
	return index.UniqueDiagnostic(h.Context(), c.parent, c.index, c.key, c.schema, output, allowed, false, c.view)
}
func checkUniqueRow(h dml.Host, ref ids.ObjectRef, candidate *index.WriteCandidate, excluded []storage.RowVersion, prefix index.WriteBatch) error {
	conflict, err := probeUniqueRow(h, ref, candidate, excluded, prefix, nil, index.ProbeRuntime)
	if err != nil {
		return err
	}
	if conflict.index != nil {
		return conflict.diagnostic(h, ref)
	}
	return nil
}

// snapshotStable reports whether the statement's isolation keeps one
// snapshot (REPEATABLE READ or SERIALIZABLE).
func snapshotStable(h dml.Host) bool {
	stable, _ := h.Snapshot()
	return stable
}

func probeUniqueRow(h dml.Host, ref ids.ObjectRef, candidate *index.WriteCandidate, excluded []storage.RowVersion, prefix index.WriteBatch, action *dml.InsertConflictState, purpose index.ProbePurpose) (uniqueConflict, error) {
	row := candidate.Values
	c := h.Command()
retry:
	binding, err := bindUniqueWrite(h, ref)
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
	evaluation := bindingEvaluation(h, binding)
	ctx := evaluation.Context
	if action != nil && action.Catalog != binding.Catalog {
		if err := action.SelectArbiters(&evaluation, parent, evaluation.Catalog); err != nil {
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
			if conflict, err := probeExclusionRow(h, ref, binding, bound, candidate, excluded, action, purpose); err != nil || conflict.index != nil {
				return conflict, err
			}
			continue
		}
		if action != nil && !slices.Contains(action.Selected, idx.Address()) {
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
		if err := h.ClaimUniqueKey(indexRef, bound, key); err != nil {
			return uniqueConflict{}, err
		}
		heads := action != nil && (action.Update != nil || snapshotStable(h) || idx.SQLIndex.HasRuntimeDomainKeyConversion())
		probe, err := h.CaptureUniqueProbe(ref, binding, idx, heads)
		if err != nil {
			return uniqueConflict{}, err
		}
		if probe.Stale {
			c.UniqueBinding = nil
			goto retry
		}
		parent, idx = probe.Parent, probe.Index
		current, root, versions := probe.View, probe.Root, probe.Versions
		if check := c.InsertConflict; check != nil {
			if err := h.CheckWriteBinding(probe.Authority, dml.BindingForConflict(ref, parent, check)); err != nil {
				return uniqueConflict{}, err
			}
		}
		if root == nil {
			return uniqueConflict{}, errors.New("stuntgres: ready unique index root is missing")
		}
		outcome := uniqueConflict{parent: parent, index: idx, key: key, auth: probe.Authority, view: current, schema: probe.Schema}
		lo, err := index.UniqueRootStart(ctx, &bound.Ordering, root.Entries, key)
		if err != nil {
			return uniqueConflict{}, err
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
			private := h.PrivateRows(ref.Address)
			if index.UniqueRowExcluded(entry.Row, excluded) || index.UniqueRowDeleted(entry.Row, private.OwnDeletes, private.StagedDeletes) {
				continue
			}
			replaced := false
			if overlay := c.WriteOverlay(); overlay != nil {
				_, replaced = overlay.AffectedRows[entry.Row]
				if !replaced {
					_, replaced = insertOwnRow(h, ref, entry.Row)
				}
			} else {
				for _, own := range private.Own {
					if own.Row == entry.Row {
						replaced = true
						break
					}
				}
				if !replaced {
					for _, own := range private.Staged {
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
		if c.WriteOverlay() != nil {
			own, found, err := probeInsertWriteOverlay(h, ref, bound, &evaluation, key, excluded, purpose)
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
			private := h.PrivateRows(ref.Address)
			for overlayIndex, overlay := range [2][]storage.RowVersion{private.Staged, private.Own} {
				for _, own := range overlay {
					fresh := h.PrivateRows(ref.Address)
					if overlayIndex == 1 {
						if _, _, replaced := storage.RowByID(fresh.Staged, own.Row, -1); replaced {
							continue
						}
					}
					if index.UniqueRowExcluded(own.Row, excluded) || index.UniqueRowDeleted(own.Row, fresh.OwnDeletes, fresh.StagedDeletes) {
						continue
					}
					equal, err := matchesExistingUnique(h, bound, own, &evaluation, key, purpose)
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

func uniqueKeyChanged(h dml.Host, ref ids.ObjectRef, old, next []sqltype.Value) (bool, error) {
	binding, err := bindUniqueWrite(h, ref)
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
