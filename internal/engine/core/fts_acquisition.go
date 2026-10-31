package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// tsAcquisition retains immutable metadata, not initialized dictionaries. Only
// actual ordered-chain consultation acquires a runtime. New receipts remain
// invocation-private until the complete conversion passes its final fence.
type tsAcquisition[A Authority[A]] struct {
	ctx      context.Context
	view     catalog.Read
	config   *catalog.TSConfiguration
	runtimes map[ids.ObjectAddress]catalog.DictionaryRuntime
	memos    map[ids.ObjectAddress]thesaurusMemo
}

func newTSAcquisition[A Authority[A]](ctx context.Context, view catalog.Read, config *catalog.TSConfiguration) *tsAcquisition[A] {
	return &tsAcquisition[A]{ctx: ctx, view: view, config: config}
}

// currentLocked checks metadata and already consulted executable inputs only.
// It never resolves a logical normalizer or initializes an unreached dictionary.
func (a *tsAcquisition[A]) currentLocked(view catalog.Read, auth A) bool {
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
		role := auth.Access().State.CurrentUser
		for address, memo := range a.memos {
			if !memo.current(view, a.view.MustTSDictionary(address), role, path) {
				return false
			}
		}
	}
	return true
}

func (a *tsAcquisition[A]) dictionary(h Host[A], address ids.ObjectAddress) (catalog.DictionaryRuntime, error) {
	if runtime := a.runtimes[address]; runtime != nil {
		return runtime, nil
	}
	runtime, err := acquireDictionaryFor(h, address, a)
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
func (a *tsAcquisition[A]) accept(ctx context.Context, h Host[A]) error {
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	if err := ctx.Err(); err != nil {
		return err
	}
	view := h.PrivateCatalogLocked(h.PendingCatalog())
	auth, err := h.AuthorizationLocked()
	if err != nil {
		return err
	}
	auth = auth.Recaptured(view)
	if !a.currentLocked(view, auth) {
		return ids.ErrCatalogReplaced
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload := h.ThesaurusMemos()
	for address, memo := range a.memos {
		if *payload == nil {
			*payload = make(ThesaurusMemos)
		}
		(*payload)[address] = memo
	}
	return nil
}
