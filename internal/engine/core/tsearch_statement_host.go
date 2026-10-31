package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// The text-search runtime's host is the statement host; AuthorizationView is
// its authority. Operation, Environments, PendingCatalog, the nested-SQL
// capability and the Engine.Mu pair are shared with exec.Host and the EXPLAIN
// renderer.
var _ Host[AuthorizationView] = (*statementHost)(nil)

func (h *statementHost) ActivateRuntime(ctx context.Context) context.Context {
	return h.scope().ActivateRuntime(ctx)
}

func (h *statementHost) PrivateCatalogLocked(changes *session.Journal) catalog.Read {
	return h.scope().Engine().PrivateCatalogLocked(changes)
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

// textSearchHost serves exec.TextSearchRuntime over the statement host. Text
// search keeps no state of its own, so each method enters the runtime with
// the host.

func (h *textSearchHost) ConvertTextSearch(ctx context.Context, kind fts.ConversionKind, explicit bool, value sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	return Convert[AuthorizationView](h.host(), ctx, kind, explicit, value, text, result)
}

func (h *textSearchHost) ConvertJSONTextSearch(ctx context.Context, explicit bool, config, document, filter sqltype.Value, filtered bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	return ConvertJSON[AuthorizationView](h.host(), ctx, explicit, config, document, filter, filtered, result)
}

func (h *textSearchHost) DebugTextSearch(ctx context.Context, explicit bool, value sqltype.Value, text string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	return Debug[AuthorizationView](h.host(), ctx, explicit, value, text, result)
}

func (h *textSearchHost) StatTextSearch(ctx context.Context, text, weights string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	return Stat[AuthorizationView](h.host(), ctx, text, weights, result)
}

func (h *textSearchHost) RewriteQuerySQL(query sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	return Rewrite[AuthorizationView](h.host(), query, text, result)
}

func (h *textSearchHost) LexizeDictionary(value sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	return Lexize[AuthorizationView](h.host(), value, text, result)
}

func (h *textSearchHost) UnaccentText(procedure ids.ObjectRef, input string, result *sqltype.Descriptor) (sqltype.Value, error) {
	return Unaccent[AuthorizationView](h.host(), procedure, input, result)
}

func (h *textSearchHost) ResolveFTSParser(value sqltype.Value) (fts.ParserRuntime, error) {
	return ResolveParser[AuthorizationView](h.host(), value)
}
