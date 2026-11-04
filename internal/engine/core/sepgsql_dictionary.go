package core

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Marks belong to the real borrowed statement, never the cached dictionary.
func (h *statementHost) MarkDictionaryInitialization() access.SepgsqlObservationMark {
	return h.scope().MarkSepgsqlObservations()
}
func (h *statementHost) AbortDictionaryInitialization(mark access.SepgsqlObservationMark) {
	h.scope().RestoreSepgsqlObservations(mark)
}

// EnterDictionaryCallback brackets OidFunctionCall's actual native body. Those
// direct calls do not invoke the SQL execute hook or check SQL EXECUTE ACL.
func (h *statementHost) EnterDictionaryCallback(binding catalog.DictionaryCallback) (sqltype.RoutineBodyToken, error) {
	s := h.scope()
	e := s.Engine()
	if err := s.Active(); err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	if e.Sepgsql == nil || e.ImportTargets != nil {
		return sqltype.RoutineBodyToken{}, nil
	}
	mark := s.MarkSepgsqlObservations()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err == nil {
		err = validateDictionaryCallback(auth.Catalog, binding)
	}
	target, transition := "", false
	if err == nil {
		target, transition, err = s.sepgsqlBodyDecision(auth, binding.Helper, false, nil)
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
func validateDictionaryCallback(view catalog.Read, binding catalog.DictionaryCallback) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid original dictionary callback binding") }
	if view.Owner() == nil || binding.Read.Owner() != view.Owner() || binding.Read.Domain() != view.Domain() || binding.Read.Epoch() != view.Epoch() || binding.Dictionary == nil || binding.Template == nil || binding.Helper == nil {
		return invalid()
	}
	d, t, p := binding.Dictionary, binding.Template, binding.Helper
	if binding.Read.Get(d.Address()) != d || binding.Read.Get(t.Address()) != t || binding.Read.Get(p.Address()) != p || view.Get(d.Address()) != d || view.Get(t.Address()) != t || view.Get(p.Address()) != p || d.Template != t.Address() {
		return ids.ErrCatalogReplaced
	}
	expected := t.Init
	switch binding.Kind {
	case catalog.DictionaryInitCallback:
	case catalog.DictionaryLexizeCallback:
		expected = t.Lexize
	default:
		return invalid()
	}
	if expected != p.Address() {
		return invalid()
	}
	if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, d); err != nil {
		return err
	}
	return (extension.CatalogProvider{Class: ids.TSTemplateClassID}).Validate(view, t)
}
