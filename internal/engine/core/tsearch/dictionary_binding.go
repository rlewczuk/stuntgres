package tsearch

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
)

// dictionaryBinding owns only captured immutable metadata/runtime. A session,
// operation, evaluator or callback token never enters a cached dictionary.
type dictionaryBinding struct {
	read         catalog.Read
	dictionary   *catalog.TSDictionary
	template     *catalog.TSTemplate
	init, lexize *catalog.Procedure
	runtime      catalog.DictionaryRuntime
	normalizer   *catalog.TSDictionary
}

func bindDictionary(view catalog.Read, d *catalog.TSDictionary, runtime catalog.DictionaryRuntime, normalizer *catalog.TSDictionary) (dictionaryBinding, error) {
	binding := dictionaryBinding{read: view, dictionary: d, runtime: runtime, normalizer: normalizer}
	template, ok := view.TSTemplate(d.Template)
	if !ok || runtime == nil {
		return dictionaryBinding{}, fmt.Errorf("stuntgres: missing dictionary invocation binding")
	}
	binding.template = template
	if template.Init.ObjectID != 0 {
		binding.init, ok = view.Procedure(template.Init)
		if !ok {
			return dictionaryBinding{}, fmt.Errorf("stuntgres: missing dictionary initialization declaration")
		}
	}
	binding.lexize, ok = view.Procedure(template.Lexize)
	if !ok {
		return dictionaryBinding{}, fmt.Errorf("stuntgres: missing dictionary lexical declaration")
	}
	return binding, nil
}
func (b dictionaryBinding) callback(kind catalog.DictionaryCallbackKind) catalog.DictionaryCallback {
	helper := b.lexize
	if kind == catalog.DictionaryInitCallback {
		helper = b.init
	}
	return catalog.DictionaryCallback{Read: b.read, Dictionary: b.dictionary, Template: b.template, Helper: helper, Kind: kind}
}
func lexizeDictionary[A Authority[A]](h Host[A], binding dictionaryBinding, ctx context.Context, text string) (result fts.LexicalResult, err error) {
	mark := h.MarkDictionaryInitialization()
	defer func() {
		if err != nil {
			h.AbortDictionaryInitialization(mark)
		}
	}()
	if err := ctx.Err(); err != nil {
		return fts.LexicalResult{}, err
	}
	token, err := h.EnterDictionaryCallback(binding.callback(catalog.DictionaryLexizeCallback))
	if err != nil {
		return fts.LexicalResult{}, err
	}
	defer token.Leave()
	if phrase, ok := binding.runtime.(catalog.PhraseDictionaryRuntime); ok {
		capture := newTSAcquisition[A](ctx, binding.read, nil)
		lexemes, err := phrase.LexizeWithNormalizer(ctx, text, &normalizerCall[A]{host: h, selected: binding.normalizer, capture: capture})
		if err != nil {
			return fts.LexicalResult{}, err
		}
		if err = capture.accept(ctx, h); err != nil {
			return fts.LexicalResult{}, err
		}
		return lexemes, nil
	}
	return binding.runtime.Lexize(ctx, text)
}

// normalizerCall is compiler/matcher-local. First actual normalization acquires
// the genuine physical binding; the cached pure dictionary never retains it.
type normalizerCall[A Authority[A]] struct {
	host     Host[A]
	selected *catalog.TSDictionary
	binding  dictionaryBinding
	capture  *tsAcquisition[A]
}

func (n *normalizerCall[A]) Lexize(ctx context.Context, text string) (fts.LexicalResult, error) {
	if err := ctx.Err(); err != nil {
		return fts.LexicalResult{}, err
	}
	if n.selected == nil {
		return fts.LexicalResult{}, fmt.Errorf("stuntgres: missing original normalizer binding")
	}
	if n.binding.runtime == nil {
		binding, err := acquireDictionaryFor(n.host, n.selected.Address(), n.capture, n.selected)
		if err != nil {
			return fts.LexicalResult{}, err
		}
		n.binding = binding
	}
	return lexizeDictionary(n.host, n.binding, ctx, text)
}

// BorrowDictionaryNormalizer creates only an invocation-local bridge. Its
// selected descriptor and host are never installed in a persistent runtime.
func BorrowDictionaryNormalizer[A Authority[A]](h Host[A], selected *catalog.TSDictionary, ctx context.Context) expr.DictionaryLexizer {
	h.ReacquireEngine()
	view := h.PrivateCatalogLocked(h.PendingCatalog())
	h.ReleaseEngineLocked()
	// Detached DDL compilation never publishes these query-session memos.
	// Its compiler owns observation rollback if any later stage fails.
	return &normalizerCall[A]{host: h, selected: selected, capture: newTSAcquisition[A](ctx, view, nil)}
}
