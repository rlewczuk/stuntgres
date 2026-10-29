package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// tsAcquisition retains immutable metadata, not initialized dictionaries. Only
// actual ordered-chain consultation acquires a runtime. New receipts remain
// invocation-private until the complete conversion passes its final fence.
type tsAcquisition struct {
	ctx      context.Context
	view     catalog.Read
	config   *catalog.TSConfiguration
	runtimes map[ids.ObjectAddress]catalog.DictionaryRuntime
	memos    map[ids.ObjectAddress]thesaurusMemo
}

func newTSAcquisition(ctx context.Context, view catalog.Read, config *catalog.TSConfiguration) *tsAcquisition {
	return &tsAcquisition{ctx: ctx, view: view, config: config}
}

// currentLocked checks metadata and already consulted executable inputs only.
// It never resolves a logical normalizer or initializes an unreached dictionary.
func (a *tsAcquisition) currentLocked(view catalog.Read, auth AuthorizationView) bool {
	if view.Domain() != a.view.Domain() || view.Epoch() != a.view.Epoch() || view.Get(a.config.Address()) != a.config || view.Get(a.config.Parser) != a.view.Get(a.config.Parser) {
		return false
	}
	parser := a.view.MustTSParser(a.config.Parser)
	for _, oid := range [...]ids.OID{parser.Start, parser.Token, parser.End, parser.Headline, parser.LexType} {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		if view.Get(address) != a.view.Get(address) {
			return false
		}
	}
	for address := range a.runtimes {
		if view.Get(address) != a.view.Get(address) {
			return false
		}
		dictionary := a.view.MustTSDictionary(address)
		template, ok := a.view.TSTemplate(dictionary.Template)
		if !ok {
			return false
		}
		for _, dependency := range [...]ids.ObjectAddress{dictionary.Template, template.Init, template.Lexize} {
			if view.Get(dependency) != a.view.Get(dependency) {
				return false
			}
		}
	}
	if len(a.memos) != 0 {
		path, err := auth.ReferencePath()
		if err != nil {
			return false
		}
		for address, memo := range a.memos {
			if !memo.current(view, a.view.MustTSDictionary(address), auth, path) {
				return false
			}
		}
	}
	return true
}

func (a *tsAcquisition) dictionary(h *statementHost, address ids.ObjectAddress) (catalog.DictionaryRuntime, error) {
	if runtime := a.runtimes[address]; runtime != nil {
		return runtime, nil
	}
	runtime, err := h.acquireDictionaryFor(address, a)
	if err != nil {
		return nil, err
	}
	if a.runtimes == nil {
		a.runtimes = make(map[ids.ObjectAddress]catalog.DictionaryRuntime)
	}
	a.runtimes[address] = runtime
	return runtime, nil
}

// accept publishes no partial receipt set. The result has already been built,
// but is not returned to the caller until this currentness/cancellation fence.
func (a *tsAcquisition) accept(ctx context.Context, h *statementHost) error {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	changes := s.Catalog
	if changes == nil {
		changes = s.Owner.Catalog
	}
	view := e.PrivateCatalogLocked(changes)
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	auth.Recapture(view)
	if !a.currentLocked(view, auth) {
		return ids.ErrCatalogReplaced
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload := PayloadOf(s.Op.Session)
	for address, memo := range a.memos {
		if payload.thesaurusMemos == nil {
			payload.thesaurusMemos = make(map[ids.ObjectAddress]thesaurusMemo)
		}
		payload.thesaurusMemos[address] = memo
	}
	return nil
}
