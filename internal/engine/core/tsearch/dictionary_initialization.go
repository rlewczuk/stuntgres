package tsearch

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

// acquireOrdinaryDictionaryLocked releases Mu on every path. Only a physical
// cold miss with a genuinely hookable Init compiles; warm receipts deliberately
// do not depend on the current subject or mutable label association.
func acquireOrdinaryDictionaryLocked[A Authority[A]](h Host[A], view catalog.Read, d *catalog.TSDictionary, capture *tsAcquisition[A]) (result dictionaryBinding, err error) {
	binding, err := bindDictionary(view, d, d.Runtime, nil)
	if err != nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, err
	}
	policy := view.Owner().Sepgsql
	if policy == nil || !policy.AnyTrustedTransitions() || binding.init == nil || routine.FmgrBuiltin(binding.init) {
		h.ReleaseEngineLocked()
		return binding, nil
	}
	payload := h.DictionaryMemos()
	if payload == nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, ids.ErrCatalogReplaced
	}
	ctx := capture.ctx
	for address, memo := range *payload {
		if memo.domain != view.Domain() || memo.epoch != view.Epoch() || view.Get(address) != memo.dictionary {
			delete(*payload, address)
		}
	}
	if memo := (*payload)[d.Address()]; memo.current(view, d, authorization.PrincipalRef{}, nil) {
		if capture.memos == nil {
			capture.memos = make(map[ids.ObjectAddress]dictionaryMemo)
		}
		capture.memos[d.Address()] = memo
		h.ReleaseEngineLocked()
		if err = ctx.Err(); err != nil {
			return dictionaryBinding{}, err
		}
		return bindDictionary(view, d, memo.runtime, nil)
	}
	files := h.DictionaryHost().DictionaryFiles()
	key := dictionaryMemo{dictionary: d, thesTemplate: binding.template, thesInit: binding.init, thesLexize: binding.lexize, domain: view.Domain(), epoch: view.Epoch()}
	h.ReleaseEngineLocked()
	mark := h.MarkDictionaryInitialization()
	defer func() {
		if err != nil {
			h.AbortDictionaryInitialization(mark)
		}
	}()
	token, err := h.EnterDictionaryCallback(binding.callback(catalog.DictionaryInitCallback))
	if err != nil {
		return dictionaryBinding{}, err
	}
	runtime, compileErr := extension.CompileDictionary(ctx, view, binding.template, d.Options, files)
	token.Leave()
	if compileErr != nil {
		return dictionaryBinding{}, compileErr
	}
	if err = ctx.Err(); err != nil {
		return dictionaryBinding{}, err
	}
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	current := h.PrivateCatalogLocked(h.PendingCatalog())
	auth, err := h.AuthorizationLocked()
	if err != nil {
		return dictionaryBinding{}, err
	}
	auth = auth.Recaptured(current)
	key.runtime = runtime
	if !key.current(current, d, authorization.PrincipalRef{}, nil) || !capture.currentLocked(current, auth) {
		return dictionaryBinding{}, ids.ErrCatalogReplaced
	}
	if err = ctx.Err(); err != nil {
		return dictionaryBinding{}, err
	}
	if capture.memos == nil {
		capture.memos = make(map[ids.ObjectAddress]dictionaryMemo)
	}
	capture.memos[d.Address()] = key
	return bindDictionary(current, d, runtime, nil)
}
