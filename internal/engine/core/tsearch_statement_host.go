package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/regobj"
)

// The text-search runtime's host is the statement host; AuthorizationView is
// its authority. Operation, Environments, the nested-SQL capability and the
// Engine.Mu pair are shared with exec.Host and the EXPLAIN renderer.
var _ Host[AuthorizationView] = (*statementHost)(nil)

func (h *statementHost) ActivateRuntime(ctx context.Context) context.Context {
	return h.scope().ActivateRuntime(ctx)
}

func (h *statementHost) PrivateCatalogLocked() catalog.Read {
	s := h.scope()
	return s.Engine().PrivateCatalogLocked(s.PendingCatalog())
}

func (h *statementHost) AuthorizationLocked() (AuthorizationView, error) {
	s := h.scope()
	return s.Engine().AuthorizationLocked(s.Op)
}

func (h *statementHost) TextSearchInputLocked() (catalog.Read, regobj.TextSearchInput, error) {
	s := h.scope()
	auth, err := s.namespaceAuthorizationLocked(s.Engine())
	if err != nil {
		return catalog.Read{}, regobj.TextSearchInput{}, err
	}
	return auth.Catalog, regobj.NewTextSearchInput(inputCapture(auth, nil)), nil
}

func (h *statementHost) DictionaryHost() extension.DictionaryHost {
	s := h.scope()
	return DDLHost{E: s.Engine(), Op: s.Op}
}

func (h *statementHost) ThesaurusMemos() *ThesaurusMemos {
	if payload := PayloadOf(h.Op.Session); payload != nil {
		return &payload.thesaurusMemos
	}
	return nil
}

// Recaptured returns v over view: the value form of View.Recapture.
func (v AuthorizationView) Recaptured(view catalog.Read) AuthorizationView {
	v.Recapture(view)
	return v
}
