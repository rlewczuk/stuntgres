package tsearch

import (
	"context"

	"stuntgres/internal/engine/access"
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
	runtimes map[ids.ObjectAddress]dictionaryBinding
	memos    map[ids.ObjectAddress]dictionaryMemo
	roles    *access.RoleRecord // This invocation's actual cold-work predecessor.
}

func newTSAcquisition[A Authority[A]](ctx context.Context, view catalog.Read, config *catalog.TSConfiguration) *tsAcquisition[A] {
	return &tsAcquisition[A]{ctx: ctx, view: view, config: config}
}

func captureDictionaryInvocation[A Authority[A]](h Host[A]) *tsAcquisition[A] {
	h.ReacquireEngine()
	view := h.PrivateCatalogLocked(h.PendingCatalog())
	h.ReleaseEngineLocked()
	return newTSAcquisition[A](h.Operation().Context(), view, nil)
}

// currentLocked checks metadata and already consulted executable inputs only.
// It never resolves a logical normalizer or initializes an unreached dictionary.
func (a *tsAcquisition[A]) currentLocked(view catalog.Read, auth A) bool {
	if view.Domain() != a.view.Domain() || view.Epoch() != a.view.Epoch() {
		return false
	}
	if a.roles != nil && a.roles != auth.Access().RoleRecord {
		return false
	}
	if a.config != nil {
		if view.Get(a.config.Address()) != a.config || view.Get(a.config.Parser) != a.view.Get(a.config.Parser) {
			return false
		}
		parser := a.view.MustTSParser(a.config.Parser)
		for _, oid := range [...]ids.OID{parser.Start, parser.Token, parser.End, parser.Headline, parser.LexType} {
			address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
			if view.Get(address) != a.view.Get(address) {
				return false
			}
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

func (a *tsAcquisition[A]) dictionary(h Host[A], address ids.ObjectAddress) (dictionaryBinding, error) {
	if runtime, ok := a.runtimes[address]; ok {
		return runtime, nil
	}
	runtime, err := acquireDictionaryFor(h, address, a, nil)
	if err != nil {
		return dictionaryBinding{}, err
	}
	if a.runtimes == nil {
		a.runtimes = make(map[ids.ObjectAddress]dictionaryBinding)
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
	payload := h.DictionaryMemos()
	for address, memo := range a.memos {
		if *payload == nil {
			*payload = make(DictionaryMemos)
		}
		(*payload)[address] = memo
	}
	return nil
}
