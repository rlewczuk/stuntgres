package dml

import (
	"errors"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// BindUniqueWrite returns the active command's unique write binding of ref,
// reusing a current one or building it against the private catalog and
// capturing its authority. It locks briefly and never waits; the caller
// holds nothing.
func BindUniqueWrite(h Host, ref ids.ObjectRef) (*index.UniqueWriteBinding, error) {
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
func bindingEvaluation(h Host, b *index.UniqueWriteBinding) exec.Context {
	input, output := h.Environments()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: h.Context(), Host: h, Input: input, Output: output, Catalog: h.CatalogRead(b.Catalog)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	if b.TypeName != nil {
		evaluation.Input.TypeDiagnosticName = b.TypeName
	}
	return evaluation
}

// ClaimUniqueRow claims the unique keys row holds on ref's exact-equality
// indexes, before the row is updated or deleted. It waits for conflicting
// claims; the caller holds nothing.
func ClaimUniqueRow(h Host, ref ids.ObjectRef, row storage.RowVersion) error {
	binding, err := BindUniqueWrite(h, ref)
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
		member, key, err := ExistingUniqueOutput(h, bound, row, &evaluation, purpose)
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

// UniqueConflict is a probe's first conflict; a zero Index means none. One
// real provider probe supplies ordinary violations, omission visibility and
// exact conflict-update identity without scanning committed table payloads.
// Row is the conflicting row's head when the probe asked for heads; Position
// is its index position, or -1 for the statement's own row.
type UniqueConflict struct {
	Parent, Index *catalog.Relation
	Key           []sqltype.Value
	Existing      []sqltype.Value
	Auth          access.View
	View          catalog.Read
	Schema        string
	Row           storage.RowVersion
	Committed     bool
	Position      int
}

// Diagnostic is the conflict's unique or exclusion violation error, with the
// key detail the actor may see.
func (c *UniqueConflict) Diagnostic(h Host, ref ids.ObjectRef) error {
	allowed, err := index.UniqueKeyDetailAllowed(c.Auth, h.DisclosureEnv(c.Auth), ref, c.Index.SQLIndex.Keys)
	if err != nil {
		return err
	}
	_, output := h.Environments()
	if c.Index.SQLIndex.IsExclusion() {
		return index.ExclusionDiagnostic(h.Context(), c.Parent, c.Index, c.Key, c.Existing, c.Schema, output, allowed, false, c.View)
	}
	return index.UniqueDiagnostic(h.Context(), c.Parent, c.Index, c.Key, c.Schema, output, allowed, false, c.View)
}

// CheckUniqueRow probes candidate against ref's unique and exclusion indexes
// and returns the violation error of the first conflict. excluded are the
// predecessors candidate replaces; prefix is the batch staged before it.
// It may wait; the caller holds nothing.
func CheckUniqueRow(h Host, ref ids.ObjectRef, candidate *index.WriteCandidate, excluded []storage.RowVersion, prefix index.WriteBatch) error {
	conflict, err := ProbeUniqueRow(h, ref, candidate, excluded, prefix, nil, index.ProbeRuntime)
	if err != nil {
		return err
	}
	if conflict.Index != nil {
		return conflict.Diagnostic(h, ref)
	}
	return nil
}

// snapshotStable reports whether the statement's isolation keeps one
// snapshot (REPEATABLE READ or SERIALIZABLE).
func snapshotStable(h Host) bool {
	stable, _ := h.Snapshot()
	return stable
}

// ProbeUniqueRow finds candidate's first conflict on ref: in the committed
// index roots, the statement's own and staged rows (through the write overlay
// when the command has one) and prefix. With action it probes only the ON
// CONFLICT arbiters and reports the conflicting row's head. It claims keys and
// may wait; the caller holds nothing.
func ProbeUniqueRow(h Host, ref ids.ObjectRef, candidate *index.WriteCandidate, excluded []storage.RowVersion, prefix index.WriteBatch, action *InsertConflictState, purpose index.ProbePurpose) (UniqueConflict, error) {
	row := candidate.Values
	c := h.Command()
retry:
	binding, err := BindUniqueWrite(h, ref)
	if err != nil {
		return UniqueConflict{}, err
	}
	if purpose == index.ProbeRuntime && binding.Checked {
		purpose = index.ProbeTracked
	}
	parent := binding.Parent
	if len(row) != len(parent.Columns) {
		return UniqueConflict{}, errors.New("stuntgres: invalid staged row width")
	}
	evaluation := bindingEvaluation(h, binding)
	ctx := evaluation.Context
	if action != nil && action.Catalog != binding.Catalog {
		if err := action.SelectArbiters(&evaluation, parent, evaluation.Catalog); err != nil {
			return UniqueConflict{}, err
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
						return UniqueConflict{}, ids.ErrCatalogReplaced
					}
				} else if err := candidate.MaterializeInsertion(insertion, &evaluation); err != nil {
					return UniqueConflict{}, err
				}
				continue
			}
			bound = insertion.Unique
		} else {
			bound = &binding.Keys[i]
		}
		idx := bound.Index
		if idx.SQLIndex.IsExclusion() {
			if conflict, err := probeExclusionRow(h, ref, binding, bound, candidate, excluded, action, purpose); err != nil || conflict.Index != nil {
				return conflict, err
			}
			continue
		}
		if action != nil && !slices.Contains(action.Selected, idx.Address()) {
			continue
		}
		member, key, err := candidate.UniqueOutput(bound, &evaluation, action != nil, purpose)
		if err != nil {
			return UniqueConflict{}, err
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
			return UniqueConflict{}, err
		}
		heads := action != nil && (action.Update != nil || snapshotStable(h) || idx.SQLIndex.HasRuntimeDomainKeyConversion())
		probe, err := h.CaptureUniqueProbe(ref, binding, idx, heads)
		if err != nil {
			return UniqueConflict{}, err
		}
		if probe.Stale {
			c.UniqueBinding = nil
			goto retry
		}
		parent, idx = probe.Parent, probe.Index
		current, root, versions := probe.View, probe.Root, probe.Versions
		if check := c.InsertConflict; check != nil {
			if err := h.CheckWriteBinding(probe.Authority, BindingForConflict(ref, parent, check)); err != nil {
				return UniqueConflict{}, err
			}
		}
		if root == nil {
			return UniqueConflict{}, errors.New("stuntgres: ready unique index root is missing")
		}
		outcome := UniqueConflict{Parent: parent, Index: idx, Key: key, Auth: probe.Authority, View: current, Schema: probe.Schema}
		lo, err := index.UniqueRootStart(ctx, &bound.Ordering, root.Entries, key)
		if err != nil {
			return UniqueConflict{}, err
		}
		for at := lo; at < len(root.Entries); at++ {
			entry := root.Entries[at]
			cmp, err := bound.Ordering.CompareKeys(ctx, entry, storage.IndexEntry{Keys: key})
			if err != nil {
				return UniqueConflict{}, err
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
					_, replaced = InsertOwnRow(h, ref, entry.Row)
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
						return UniqueConflict{}, err
					}
					if purpose != index.ProbeRevalidation && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
						if err := bound.FillKeys(head.Values, bound.Scratch, &evaluation); err != nil {
							return UniqueConflict{}, err
						}
					}
					outcome.Row = head
					outcome.Position = entry.Position
					outcome.Committed = head.Born != 0
				}
				return outcome, nil
			}
		}
		if c.WriteOverlay() != nil {
			own, found, err := probeInsertWriteOverlay(h, ref, bound, &evaluation, key, excluded, purpose)
			if err != nil {
				return UniqueConflict{}, err
			}
			if found {
				if purpose != index.ProbeRevalidation && action != nil && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
					if err := bound.FillKeys(own.Values, bound.Scratch, &evaluation); err != nil {
						return UniqueConflict{}, err
					}
				}
				outcome.Row, outcome.Position = own, -1
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
						return UniqueConflict{}, err
					}
					if equal {
						if purpose != index.ProbeRevalidation && action != nil && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
							if err := bound.FillKeys(own.Values, bound.Scratch, &evaluation); err != nil {
								return UniqueConflict{}, err
							}
						}
						outcome.Row = own
						outcome.Position = -1
						return outcome, nil
					}
				}
			}
		}
		for i := range prefix.Values {
			accepted := prefix.Candidate(i)
			equal, err := accepted.MatchesUnique(bound, &evaluation, key)
			if err != nil {
				return UniqueConflict{}, err
			}
			if equal {
				return outcome, nil
			}
		}
	}
	return UniqueConflict{}, nil
}

// UniqueKeyChanged reports whether next changes any unique key column of old.
func UniqueKeyChanged(h Host, ref ids.ObjectRef, old, next []sqltype.Value) (bool, error) {
	binding, err := BindUniqueWrite(h, ref)
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
