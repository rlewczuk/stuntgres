package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/tsearch"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var _ extension.DictionaryCompiler = DDLHost{}

// EnterPreparedDictionaryInit owns the real detached DDL compiler invocation.
// Its selected callback is authenticated against this open stage and the fresh
// physical subject; a captured preparation never supplies authorization.
func (h DDLHost) EnterPreparedDictionaryInit(p *extension.DictionaryPreparation) (sqltype.RoutineBodyToken, access.SepgsqlObservationMark, error) {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid detached dictionary initialization owner") }
	if h.E == nil || h.Op == nil || p == nil {
		return sqltype.RoutineBodyToken{}, access.SepgsqlObservationMark{}, invalid()
	}
	e := h.E
	if e.Sepgsql == nil || e.ImportTargets != nil {
		return sqltype.RoutineBodyToken{}, access.SepgsqlObservationMark{}, nil
	}
	s := ActiveScopeOfLease(h.Op.Lease)
	if s == nil || s.Op != h.Op || s.Engine() != e {
		return sqltype.RoutineBodyToken{}, access.SepgsqlObservationMark{}, invalid()
	}
	mark := s.MarkSepgsqlObservations()
	e.Mu.Lock()
	stage := h.CatalogStage
	var err error
	if stage == nil || stage.Owner != e || stage.Lifecycle != CatalogOpen || stage.Invalid != nil || stage.statement != s || stage.AuthSession != h.Op.Session {
		err = invalid()
	}
	if err == nil {
		err = h.Op.Context().Err()
	}
	var auth AuthorizationView
	if err == nil {
		auth, err = s.RoutineAuthorizationLocked()
	}
	if err == nil && (stage.Base.State() != auth.Catalog.State() || p.View.Owner() != auth.Catalog.Owner() || p.View.Domain() != auth.Catalog.Domain() || p.View.Epoch() != auth.Catalog.Epoch() || !p.Current(e.StageRead(stage))) {
		err = ids.ErrCatalogReplaced
	}
	var helper *catalog.Procedure
	if err == nil && p.Template != nil {
		if auth.Catalog.Get(p.Template.Address()) != p.Template {
			err = ids.ErrCatalogReplaced
		} else {
			err = (extension.CatalogProvider{Class: ids.TSTemplateClassID}).Validate(auth.Catalog, p.Template)
		}
		if err == nil && p.Original != nil && auth.Catalog.Get(p.Original.Address()) != p.Original {
			err = ids.ErrCatalogReplaced
		}
		if err == nil && p.Template.Init.ObjectID != 0 {
			var ok bool
			helper, ok = p.View.Procedure(p.Template.Init)
			if !ok || auth.Catalog.Get(p.Template.Init) != helper {
				err = ids.ErrCatalogReplaced
			}
		}
	}
	target, transition := "", false
	if err == nil && helper != nil {
		target, transition, err = s.sepgsqlBodyDecision(auth, helper, false, nil)
	}
	e.Mu.Unlock()
	if err == nil && !stage.dictionaryInitializationOwned {
		stage.dictionaryInitializationMark = mark
		stage.dictionaryInitializationOwned = true
	}
	if err != nil {
		s.RestoreSepgsqlObservations(mark)
		return sqltype.RoutineBodyToken{}, mark, err
	}
	if !transition {
		return sqltype.RoutineBodyToken{}, mark, nil
	}
	return h.Op.Session.Sepgsql.Enter(target), mark, nil
}

func (h DDLHost) AbortDictionaryInitialization(mark access.SepgsqlObservationMark) {
	if h.E == nil || h.E.Sepgsql == nil || h.E.ImportTargets != nil {
		return
	}
	scope := ActiveScopeOfLease(h.Op.Lease)
	if scope == nil || scope.Op != h.Op || scope.Engine() != h.E || h.CatalogStage == nil || h.CatalogStage.statement != scope || h.CatalogStage.AuthSession != h.Op.Session {
		panic("stuntgres: foreign dictionary initialization cleanup")
	}
	scope.RestoreSepgsqlObservations(mark)
}

func (h DDLHost) BorrowDictionaryNormalizer(ctx context.Context, p *extension.DictionaryPreparation) expr.DictionaryLexizer {
	if p == nil || p.Thesaurus == nil || p.Thesaurus.Normalizer == nil {
		return nil
	}
	// Without an active simulator there are no dictionary callback policy effects;
	// preserve the existing pure compiler and its captured resource precedence.
	if h.E.Sepgsql == nil || h.E.ImportTargets != nil {
		return p.Thesaurus.Normalizer.Runtime
	}
	scope := ActiveScopeOfLease(h.Op.Lease)
	if scope == nil || scope.Op != h.Op || scope.Engine() != h.E {
		return nil
	}
	return tsearch.BorrowDictionaryNormalizer[AuthorizationView]((*statementHost)(scope), p.Thesaurus.Normalizer, ctx)
}
