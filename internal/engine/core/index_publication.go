package core

import (
	"context"
	"errors"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func (e *Engine) EvolveStageIndexesLocked(stage *CatalogStage, typeParent ids.ObjectAddress, transformations []index.ColumnTransformation) error {
	// Walk the base relations without allocating: an unrelated staged
	// relation must not allocate an adapter/iterator/yield closure. The
	// selector reads the stage itself, and a provider exists only for an
	// affected index.
	for address, descriptor := range stage.Base.Relations() {
		idx, parent, staged, affected := index.EvolutionInput(stage, address, descriptor)
		if !affected {
			continue
		}
		provider := &DropProvider{Base: stage.Base, Owner: stage.Owner, SearchPath: stage.DependencyPath, Stage: stage, Ctx: context.Background()}
		if err := index.ApplyIndexEvolution(provider, address, idx, parent, staged, typeParent, transformations); err != nil {
			return catalog.RenderDefaultClassAmbiguity(err, catalog.TypeDiagnosticNamer(stage.Base, stage.DependencyPath))
		}
	}
	return nil
}

// Constructors poll cancellation while doing index work; the publication owner
// performs the final veto. An empty/unchanged set must not add an Err callback
// between the owner's established successful observation and installation.
func (e *Engine) prepareIndexCatalog(evaluation *exec.Context, current, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*catalog.Relation, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]index.RootCandidate, progress index.ProgressBinding) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	return e.prepareIndexRoots(evaluation, current, next, relations, roots, rebuildParents, rebuildIndexes, nil, true, output, auth, runtimeOwners, candidates, progress)
}

// prepareIndexRoots reads next, recaptures auth over it, and prepares the
// index publication. A nil auth is trusted replacement: catalog identity but
// no SQL principal.
func (e *Engine) prepareIndexRoots(evaluation *exec.Context, current, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*catalog.Relation, publications []index.RowPublication, materialize bool, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]index.RootCandidate, progress index.ProgressBinding) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	view := e.Catalog.Read(next)
	disclosure := access.View{Catalog: view}
	var env index.DisclosureEnv
	var recaptured *AuthorizationView
	if auth != nil {
		copy := *auth
		recaptured = &copy
		recaptured.Catalog = view
		recaptured.Recapture(recaptured.Catalog)
		disclosure, env = recaptured.View, DisclosureEnv(recaptured)
	}
	roots, err := index.PrepareIndexPublication(evaluation, current, next, view, relations, roots, rebuildParents, rebuildIndexes, publications, materialize, output, disclosure, env, runtimeOwners, candidates, progress)
	if auth == nil || err == nil {
		return roots, err
	}
	var ambiguity *catalog.DefaultClassAmbiguity
	if errors.As(err, &ambiguity) {
		name, nameErr := CaptureTypeDiagnosticName(*recaptured)
		if nameErr != nil {
			return nil, nameErr
		}
		err = catalog.RenderDefaultClassAmbiguity(err, name)
	}
	return roots, err
}

// Explicit statements must discover derived-key errors before installing private
// rows, not defer them to COMMIT. Admission owns these rows; the captured catalog
// and execution trees are immutable. Finish retains its final publication veto.
func (s *StatementScope) validatePrivateIndexExpressions(changes *session.Journal, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes) error {
	if len(s.Stage.Rows) == 0 {
		return nil
	}
	e := s.Engine()
	e.Mu.Lock()
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	view := e.PrivateCatalogLocked(changes)
	input := s.Input
	var programmeAuth AuthorizationView
	if len(s.Domains.PreparedChecks) != 0 || len(s.routinePrograms) != 0 || len(s.IndexBindings) != 0 {
		var err error
		programmeAuth, err = s.writeAuthorizationLocked(e)
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		programmeAuth.Recapture(view)
	}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || !index.SQLIndex.Ready || !index.SQLIndex.Live || !index.SQLIndex.HasRuntimeDomainConversion() || len(s.Stage.Rows[index.SQLIndex.Parent]) == 0 {
			continue
		}
		auth := programmeAuth
		var err error
		if !auth.live() {
			auth, err = s.writeAuthorizationLocked(e)
		}
		if err == nil {
			auth.Catalog = view
			auth.Recapture(view)
			input.TypeDiagnosticName, err = CaptureTypeDiagnosticName(auth)
		}
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		break
	}
	e.Mu.Unlock()
	if programmeAuth.live() {
		if err := s.readmitPreparedPrograms(programmeAuth); err != nil {
			return err
		}
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.Op.Context(), Host: HostOf(s), Input: input, Output: s.Output, Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	return index.ValidatePrivateExpressions(&evaluation, view, s.Stage.Rows, deletes)
}
