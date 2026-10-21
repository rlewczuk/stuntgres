package engine

import (
	"context"
	"maps"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func (e *Engine) evolveStageIndexesLocked(stage *CatalogStage, typeParent ids.ObjectAddress, transformations []columnTransformation) error {
	// Preserve the incumbent map walk. In particular an unrelated staged
	// relation must not allocate an adapter/iterator/yield closure: the
	// selector reads the stage itself, and a provider exists only for an
	// affected index.
	for address, descriptor := range stage.Base.Descriptors() {
		index, parent, staged, affected := indexEvolutionInput(stage, address, descriptor)
		if !affected {
			continue
		}
		provider := &dropProvider{base: stage.Base, owner: stage.owner, path: stage.dependencyPath, stage: stage, ctx: context.Background()}
		if err := applyIndexEvolution(provider, address, index, parent, staged, typeParent, transformations); err != nil {
			return err
		}
	}
	return nil
}

// Constructors poll cancellation while doing index work; the publication owner
// performs the final veto. An empty/unchanged set must not add an Err callback
// between the owner's established successful observation and installation.
func (e *Engine) prepareIndexCatalog(evaluation *exec.Context, current, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*catalog.Relation, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]indexRootCandidate) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	return e.prepareIndexRoots(evaluation, current, next, relations, roots, rebuildParents, rebuildIndexes, nil, true, output, auth, runtimeOwners, candidates)
}

// prepareIndexRoots reads next, recaptures auth over it, and prepares the
// index publication. A nil auth is trusted replacement: catalog identity but
// no SQL principal.
func (e *Engine) prepareIndexRoots(evaluation *exec.Context, current, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*catalog.Relation, publications []rowPublication, materialize bool, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]indexRootCandidate) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	view := e.catalog.read(next)
	disclosure := access.View{Catalog: view}
	var env indexDisclosureEnv
	if auth != nil {
		recaptured := *auth
		recaptured.Catalog = view
		recaptured.Recapture(recaptured.Catalog)
		disclosure, env = recaptured.View, disclosureEnv(&recaptured)
	}
	return prepareIndexPublication(evaluation, current, next, view, relations, roots, rebuildParents, rebuildIndexes, publications, materialize, output, disclosure, env, runtimeOwners, candidates)
}

// Explicit statements must discover derived-key errors before installing private
// rows, not defer them to COMMIT. Admission owns these rows; the captured catalog
// and execution trees are immutable. Finish retains its final publication veto.
func (s *StatementScope) validatePrivateIndexExpressions(changes *session.Journal, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes) error {
	if len(s.stage.rows) == 0 {
		return nil
	}
	e := s.engine()
	e.mu.Lock()
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(changes)
	input := s.input
	var programmeAuth AuthorizationView
	if len(s.domains.preparedChecks) != 0 || len(s.routinePrograms) != 0 || len(s.indexBindings) != 0 {
		var err error
		programmeAuth, err = s.writeAuthorizationLocked(e)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		programmeAuth.Recapture(view)
	}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || !index.SQLIndex.Ready || !index.SQLIndex.Live || !index.SQLIndex.HasRuntimeDomainConversion() || len(s.stage.rows[index.SQLIndex.Parent]) == 0 {
			continue
		}
		auth := programmeAuth
		var err error
		if auth.engine == nil {
			auth, err = s.writeAuthorizationLocked(e)
		}
		if err == nil {
			auth.Catalog = view
			auth.Recapture(view)
			input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(auth)
		}
		if err != nil {
			e.mu.Unlock()
			return err
		}
		break
	}
	e.mu.Unlock()
	if programmeAuth.engine != nil {
		if err := s.readmitPreparedPrograms(programmeAuth); err != nil {
			return err
		}
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Input: input, Output: s.output, Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || !index.SQLIndex.Ready || !index.SQLIndex.Live {
			continue
		}
		rows := s.stage.rows[index.SQLIndex.Parent]
		if len(rows) == 0 {
			continue
		}
		if index.SQLIndex.Predicate == nil {
			derived := false
			for _, key := range index.SQLIndex.Keys {
				derived = derived || key.Derived != nil
			}
			if !derived {
				continue
			}
		}
		pending := index.SQLIndex.HasRuntimeDomainConversion()
		var ordering indexOrdering
		var hash storage.HashBinding
		var preparation indexExecutionPreparation
		parent, ok := view.Relation(index.SQLIndex.Parent)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if pending {
			var err error
			if index.SQLIndex.Method == catalog.HashMethod {
				hash, err = bindHashIndex(parent, index.SQLIndex)
			} else {
				ordering, err = bindIndexKeys(parent, index.SQLIndex)
			}
			if err != nil {
				return err
			}
		}
		copied := false
		for i, row := range rows {
			if !rowMaintainsIndex(row, index) {
				continue
			}
			if !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), nil, deletes[index.SQLIndex.Parent]) {
				continue
			}
			if output, ok := row.IndexOutputs[index.Address()]; ok && indexOutputMatches(output, index) {
				// The SQL producer already materialized this exact version's
				// membership/key under the captured index semantics.
				continue
			}
			if pending {
				output, err := materializeIndexOutput(&evaluation, &preparation, parent, index, &ordering, &hash, row.Values)
				if err != nil {
					return err
				}
				outputs := maps.Clone(row.IndexOutputs)
				if outputs == nil {
					outputs = make(map[ids.ObjectAddress]storage.IndexOutput)
				}
				outputs[index.Address()] = output
				if !copied {
					rows = slices.Clone(rows)
					copied = true
				}
				rows[i].IndexOutputs = outputs
				continue
			}
			matches, err := preparation.predicateMatches(&evaluation, parent, index.SQLIndex, row.Values, nil, nil)
			if err != nil {
				return err
			}
			if !matches {
				continue
			}
			if err := preparation.prepareOrdinaryKeys(&evaluation, parent, index.SQLIndex); err != nil {
				return err
			}
			evaluation.Row = row.Values
			for keyIndex, key := range index.SQLIndex.Keys {
				if key.Derived == nil {
					continue
				}
				if _, err := preparation.ordinary[keyIndex].Eval(&evaluation); err != nil {
					return err
				}
			}
		}
		if copied {
			s.stage.rows[index.SQLIndex.Parent] = rows
		}
	}
	return nil
}
