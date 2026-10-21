package dml

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func publishExclusionCandidate(h Host, ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, proposal *index.WriteCandidate, excluded []storage.RowVersion, precheck bool, purpose index.ProbePurpose) (storage.RowVersion, error) {
	evaluation := bindingEvaluation(h, binding)
	definition := index.ExclusionDefinition(binding.Parent, bound.Index)
	if definition == nil {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	member, keys, err := proposal.UniqueOutput(bound, &evaluation, precheck, purpose)
	if err != nil {
		return storage.RowVersion{}, err
	}
	member = member && !index.UniqueHasNull(keys)
	if member && (purpose == index.ProbeRuntime && !bound.Index.SQLIndex.HasRuntimeDomainConversion() || bound.Index.SQLIndex.Method == catalog.HashMethod) {
		keys = slices.Clone(keys)
	}
	if purpose == index.ProbeRevalidation && proposal.Exclusion == nil {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	row, err := h.PublishExclusion(ref, binding.Catalog, *proposal, index.ExclusionEdit{Index: definition.Index, Keys: keys, Member: member, Admitted: definition.Deferrable, Precheck: precheck, Excluded: excluded})
	if err != nil {
		return storage.RowVersion{}, err
	}
	proposal.RecordExclusion(row, precheck)
	return row, nil
}

// DiscardExclusion drops c's reserved exclusion identity and its projected
// entries, when it has one.
func DiscardExclusion(h Host, c *index.WriteCandidate) {
	if version := c.TakeExclusion(); version != 0 {
		h.DiscardExclusion(version)
	}
}

// ProbeExclusion compares candidate's own published exclusion entry on
// bound's index with every other visible entry, waiting for an undecided
// owner; recheck is a deferred recheck (SET CONSTRAINTS or commit). It also
// reports whether a deferred conflict is possible. The caller holds nothing.
func ProbeExclusion(h Host, ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, candidate storage.RowVersion, recheck bool, purpose index.ProbePurpose) (UniqueConflict, bool, error) {
	idx := bound.Index
	for {
		scan, err := h.CaptureExclusionScan(ref, binding, idx)
		if err != nil {
			return UniqueConflict{}, false, err
		}
		ticket := h.Ticket()
		current, found := index.OwnExclusionEntry(&scan, ticket, idx.Address(), candidate)
		if !found {
			return UniqueConflict{}, false, nil
		}
		input, output := h.Environments()
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: h.Context(), Input: input, Output: output, Host: h, Catalog: scan.View}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if binding.TypeName != nil {
			evaluation.Input.TypeDiagnosticName = binding.TypeName
		}
		visit, err := index.VisitExclusion(h, &evaluation, &scan, ticket, ref.Address, bound, current, purpose)
		if err != nil {
			return UniqueConflict{}, false, err
		}
		if !h.ExclusionScanCurrent(scan, idx.Address()) {
			continue
		}
		deferred, err := index.ExclusionDeferred(binding.Parent, idx, recheck)
		if err != nil {
			return UniqueConflict{}, false, err
		}
		if deferred {
			return UniqueConflict{}, visit.Potential, nil
		}
		if visit.Wait != nil {
			if err := h.WaitTransaction(visit.Wait, lock.CompletionTransaction); err != nil {
				return UniqueConflict{}, false, err
			}
			continue
		}
		if !visit.Hit {
			return UniqueConflict{}, visit.Potential, nil
		}
		return UniqueConflict{Parent: binding.Parent, Index: idx, Key: visit.Key, Existing: visit.Existing, Auth: scan.Authority, View: scan.View, Schema: scan.Schema, Row: visit.Row, Committed: visit.Committed}, visit.Potential, nil
	}
}

func probeExclusionRow(h Host, ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, proposal *index.WriteCandidate, excluded []storage.RowVersion, action *InsertConflictState, purpose index.ProbePurpose) (UniqueConflict, error) {
	idx := bound.Index
	if action != nil && !slices.Contains(action.Selected, idx.Address()) {
		return UniqueConflict{}, nil
	}
	candidate, err := publishExclusionCandidate(h, ref, binding, bound, proposal, excluded, action != nil, purpose)
	if err != nil {
		return UniqueConflict{}, err
	}
	c := index.ExclusionDefinition(binding.Parent, idx)
	if c == nil {
		return UniqueConflict{}, ids.ErrCatalogReplaced
	}
	if action != nil && c.Deferrable {
		schema := binding.Catalog.Get(binding.Parent.Namespace()).Name()
		return UniqueConflict{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "ON CONFLICT does not support deferrable unique constraints/exclusion constraints as arbiters", Schema: schema, Table: binding.Parent.Name(), Constraint: idx.Name()}
	}
	if !c.Deferrable {
		// Existing exact-key grants serialize nondeferrable equal admissions.
		keys := h.OwnExclusionKeys(idx.Address(), candidate.Version)
		if keys == nil {
			return UniqueConflict{}, nil
		}
		indexRef := ref
		indexRef.Address = idx.Address()
		if idx.SQLIndex.ExactEqualityClaims() {
			if err := h.ClaimUniqueKey(indexRef, bound, keys); err != nil {
				return UniqueConflict{}, err
			}
		}
		h.AdmitExclusion(idx.Address(), candidate.Version)
	}
	conflict, potential, err := ProbeExclusion(h, ref, binding, bound, candidate, false, purpose)
	if err != nil {
		return UniqueConflict{}, err
	}
	if conflict.Index != nil {
		return conflict, nil
	}
	if c.Deferrable && potential {
		event := index.ExclusionEvent{Parent: ref, Index: ref.Domain().Ref(ref.Epoch, idx.Address()), Constraint: c.OID, Row: candidate.Row, Version: candidate.Version}
		var events []index.ExclusionEvent
		if scope := h.IndexScope(false); scope != nil {
			events = scope.ExclusionModes.Events
		}
		if !slices.Contains(events, event) {
			h.IndexScope(true).ExclusionModes.Events = append(slices.Clone(events), event)
			if actions := h.ForeignActions(false); actions != nil && actions.Settling {
				actions.ExclusionAdded = true
			}
		}
	}
	return UniqueConflict{}, nil
}
