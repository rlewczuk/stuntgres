package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// stagedDictionaryInitLocked accepts only this statement's complete, sealed,
// genuinely new unaccent installation. No caller-selected read is authority.
func (s *StatementScope) stagedDictionaryInitLocked(stage *CatalogStage, d *catalog.TSDictionary, t *catalog.TSTemplate, p *catalog.Procedure) (AuthorizationView, catalog.Read, error) {
	invalid := func() (AuthorizationView, catalog.Read, error) {
		return AuthorizationView{}, catalog.Read{}, fmt.Errorf("stuntgres: invalid staged dictionary initialization owner")
	}
	if s == nil || s.Op == nil || s.Finished || stage == nil || stage.Owner != s.Engine() || stage.Lifecycle != CatalogOpen || stage.Invalid != nil || stage.statement != s || stage.AuthSession != s.Op.Session || ActiveScopeOfLease(s.Op.Lease) != s || d == nil || t == nil || p == nil {
		return invalid()
	}
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return AuthorizationView{}, catalog.Read{}, err
	}
	if stage.Base.Owner() != auth.Catalog.Owner() || stage.Base.Domain() != auth.Catalog.Domain() || stage.Base.Epoch() != auth.Catalog.Epoch() {
		return AuthorizationView{}, catalog.Read{}, ids.ErrCatalogReplaced
	}
	if s.Engine().Sepgsql != nil && stage.Base.State() != auth.Catalog.State() {
		return AuthorizationView{}, catalog.Read{}, ids.ErrCatalogReplaced
	}
	view := s.Engine().StageRead(stage)
	if view.Get(d.Address()) != d || view.Get(t.Address()) != t || view.Get(p.Address()) != p || d.Template != t.Address() || t.Init != p.Address() || stage.Base.Get(d.Address()) != nil || stage.Base.Get(t.Address()) != nil || stage.Base.Get(p.Address()) != nil {
		return invalid()
	}
	origin := catalog.DescriptorOrigin(d)
	if origin == nil || origin.Installation == nil || origin.Installation.Provider != "unaccent" || origin.Installation.Implementation != extension.StockUnaccent || !catalog.SameInstallation(t, origin.Installation) || !catalog.SameInstallation(p, origin.Installation) {
		return invalid()
	}
	installed, ok := view.Extension(origin.Installation.Cohort)
	if !ok || installed.Installation != origin.Installation {
		return invalid()
	}
	if err = extension.ValidateCurrentInstallation(view, installed, extension.StockUnaccent); err != nil {
		return AuthorizationView{}, catalog.Read{}, err
	}
	if err = (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, d); err != nil {
		return AuthorizationView{}, catalog.Read{}, err
	}
	// Only the authenticated owner's actual candidate may substitute for the
	// current journal, so new helper labels are visible without borrowed grants.
	auth.Recapture(view)
	return auth, view, nil
}

func (s *StatementScope) EnterStagedDictionaryInit(stage *CatalogStage, d *catalog.TSDictionary, t *catalog.TSTemplate, p *catalog.Procedure) (sqltype.RoutineBodyToken, error) {
	e := s.Engine()
	mark := s.MarkSepgsqlObservations()
	e.Mu.Lock()
	auth, _, err := s.stagedDictionaryInitLocked(stage, d, t, p)
	target, transition := "", false
	if err == nil && e.Sepgsql != nil && e.ImportTargets == nil {
		target, transition, err = s.sepgsqlBodyDecision(auth, p, false, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		s.RestoreSepgsqlObservations(mark)
		return sqltype.RoutineBodyToken{}, err
	}
	if !transition {
		return sqltype.RoutineBodyToken{}, nil
	}
	return s.Op.Session.Sepgsql.Enter(target), nil
}

// InitializeStagedDictionary runs the script's actual initialization outside
// Engine.Mu. The sealed canonical descriptor/runtime is never rewritten.
func (s *StatementScope) InitializeStagedDictionary(stage *CatalogStage, d *catalog.TSDictionary) (err error) {
	if s == nil || s.Op == nil || stage == nil || d == nil {
		return fmt.Errorf("stuntgres: missing staged dictionary initialization owner")
	}
	e := s.Engine()
	mark := s.MarkSepgsqlObservations()
	defer func() {
		if err != nil {
			s.RestoreSepgsqlObservations(mark)
		}
	}()
	e.Mu.Lock()
	view := e.StageRead(stage)
	t, ok := view.TSTemplate(d.Template)
	var p *catalog.Procedure
	if ok {
		p, ok = view.Procedure(t.Init)
	}
	if !ok {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: missing staged dictionary initializer")
	}
	auth, view, err := s.stagedDictionaryInitLocked(stage, d, t, p)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	roles := auth.RoleRecord
	base := stage.Base.State()
	labelChanges := stage.SecurityLabelChangeCount()
	files := (DDLHost{E: e, Op: s.Op, CatalogStage: stage}).DictionaryFiles()
	e.Mu.Unlock()
	token, err := s.EnterStagedDictionaryInit(stage, d, t, p)
	if err != nil {
		return err
	}
	runtime, compileErr := func() (catalog.DictionaryRuntime, error) {
		defer token.Leave()
		return extension.CompileDictionary(s.Op.Context(), view, t, d.Options, files)
	}()
	if compileErr != nil {
		return compileErr
	}
	if runtime != functions.StockUnaccent || runtime != d.Runtime {
		return fmt.Errorf("stuntgres: staged dictionary compiler disagrees with canonical installation")
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	current, currentView, err := s.stagedDictionaryInitLocked(stage, d, t, p)
	if err != nil {
		return err
	}
	if stage.Base.State() != base || current.RoleRecord != roles || stage.SecurityLabelChangeCount() != labelChanges || (DDLHost{E: e, Op: s.Op, CatalogStage: stage}).DictionaryFiles() != files {
		return ids.ErrCatalogReplaced
	}
	// StageRead materializes a fresh persistent label root for the same own
	// changes. Fence its exact records, not that transient root identity.
	// Candidate label keys never disappear, including nil removal records.
	for key, record := range stage.SecurityLabelChanges() {
		if currentView.State().SecurityLabel(key) != record || view.State().SecurityLabel(key) != record {
			return ids.ErrCatalogReplaced
		}
	}
	if err = s.Op.Context().Err(); err != nil {
		return err
	}
	if err = s.validateSepgsqlObservations(e.Catalog.State); err != nil {
		return err
	}
	if e.Sepgsql != nil && !stage.dictionaryInitializationOwned {
		stage.dictionaryInitializationMark = mark
		stage.dictionaryInitializationOwned = true
	}
	return nil
}
