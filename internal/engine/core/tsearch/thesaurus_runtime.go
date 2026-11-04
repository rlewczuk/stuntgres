package tsearch

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// A successful initialization belongs to one physical session. Namespace USAGE
// authorizes initialization, not each invocation of an unchanged initialized
// dictionary. Exact executable input changes require new initialization.
type dictionaryMemo struct {
	dictionary              *catalog.TSDictionary
	normalizer              *catalog.TSDictionary
	template                *catalog.TSTemplate
	thesTemplate            *catalog.TSTemplate
	thesInit, thesLexize    catalog.Descriptor
	init, lexize            catalog.Descriptor
	runtime                 catalog.DictionaryRuntime
	role                    authorization.PrincipalRef
	path                    []ids.ObjectAddress
	domain                  *ids.Domain
	epoch                   ids.CatalogEpoch
	normalizerNamespaceName string
}

// current checks initialized execution inputs, not namespace ACL revisions. A
// miss is never permission to reuse old state: acquisition must resolve and
// authorize the logical normalizer again before installing a replacement.
func (m dictionaryMemo) current(view catalog.Read, d *catalog.TSDictionary, role authorization.PrincipalRef, path []ids.ObjectAddress) bool {
	if m.runtime == nil || m.dictionary != d || m.domain != view.Domain() || m.epoch != view.Epoch() || m.thesTemplate == nil {
		return false
	}
	if view.Get(d.Template) != m.thesTemplate || view.Get(m.thesTemplate.Init) != m.thesInit || view.Get(m.thesTemplate.Lexize) != m.thesLexize {
		return false
	}
	if m.normalizer == nil {
		return m.template == nil
	}
	if m.template == nil || m.role != role || !slices.Equal(m.path, path) {
		return false
	}
	if view.Get(m.normalizer.Address()) != m.normalizer || view.Get(m.normalizer.Template) != m.template || view.Get(m.template.Init) != m.init || view.Get(m.template.Lexize) != m.lexize {
		return false
	}
	namespace, ok := view.Namespace(m.normalizer.Namespace())
	return ok && namespace.Name() == m.normalizerNamespaceName && view.NameAddress(catalog.DescriptorNameKey(m.normalizer)) == m.normalizer.Address()
}

// acquireDictionary runs resource parsing and callbacks outside Engine.Mu.
// Only successful session initialization is retained; errors never install state
// or permit a stale initialized runtime after executable inputs have changed.
func acquireDictionaryFor[A Authority[A]](h Host[A], address ids.ObjectAddress, capture *tsAcquisition[A], selected *catalog.TSDictionary) (result dictionaryBinding, err error) {
	if capture == nil {
		return dictionaryBinding{}, fmt.Errorf("stuntgres: missing dictionary acquisition owner")
	}
	mark := h.MarkDictionaryInitialization()
	defer func() {
		if err != nil {
			h.AbortDictionaryInitialization(mark)
		}
	}()
	payload := h.DictionaryMemos()
	if payload == nil {
		return dictionaryBinding{}, ids.ErrCatalogReplaced
	}
	ctx := capture.ctx
	if err := ctx.Err(); err != nil {
		return dictionaryBinding{}, err
	}
	h.ReacquireEngine()
	auth, err := h.AuthorizationLocked()
	if err != nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, err
	}
	changes := h.PendingCatalog()
	view := h.PrivateCatalogLocked(changes)
	auth = auth.Recaptured(view)
	if !capture.currentLocked(view, auth) || view.Get(address) != capture.view.Get(address) {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, ids.ErrCatalogReplaced
	}
	if capture.roles == nil {
		capture.roles = auth.Access().RoleRecord
	}
	d, ok := view.TSDictionary(address)
	if !ok {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, sqlError("XX000", fmt.Sprintf("cache lookup failed for text search dictionary %d", address.ObjectID))
	}
	if selected != nil && selected != d {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, ids.ErrCatalogReplaced
	}
	if err = (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, d); err != nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, err
	}
	if !extension.IsThesaurus(view, d) {
		return acquireOrdinaryDictionaryLocked(h, view, d, capture)
	}
	path, err := auth.ReferencePath()
	if err != nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, err
	}
	for a, memo := range *payload {
		if view.Get(a) != memo.dictionary || memo.domain != view.Domain() || memo.epoch != view.Epoch() {
			delete(*payload, a)
		}
	}
	if memo := (*payload)[address]; memo.current(view, d, auth.Access().State.CurrentUser, path) {
		if !memo.current(capture.view, d, auth.Access().State.CurrentUser, path) {
			h.ReleaseEngineLocked()
			return dictionaryBinding{}, ids.ErrCatalogReplaced
		}
		if capture.memos == nil {
			capture.memos = make(map[ids.ObjectAddress]dictionaryMemo)
		}
		capture.memos[address] = memo
		h.ReleaseEngineLocked()
		if err := ctx.Err(); err != nil {
			return dictionaryBinding{}, err
		}
		return bindDictionary(view, d, memo.runtime, memo.normalizer)
	}
	template, _ := view.TSTemplate(d.Template)
	input, err := extension.CaptureThesaurusInput(h.DictionaryHost(), auth, template, d.Options)
	if err != nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, err
	}
	key := dictionaryMemo{dictionary: d, normalizer: input.Normalizer, template: input.Template, thesTemplate: template, thesInit: view.Get(template.Init), thesLexize: view.Get(template.Lexize), role: auth.Access().State.CurrentUser, path: slices.Clone(path), domain: view.Domain(), epoch: view.Epoch()}
	if input.Template != nil {
		key.init, key.lexize = view.Get(input.Template.Init), view.Get(input.Template.Lexize)
	}
	if input.Normalizer != nil {
		key.normalizerNamespaceName = view.MustNamespace(input.Normalizer.Namespace()).Name()
	}
	// Demand resolves both snapshots, never during metadata capture.
	// This also fences a missing/replaced logical source before exposing
	// a compiler error from a different generation.
	capturedAuth := auth.Recaptured(capture.view)
	capturedInput, captureErr := extension.CaptureThesaurusInput(h.DictionaryHost(), capturedAuth, template, d.Options)
	key.runtime = d.Runtime
	valid := captureErr == nil && capturedInput != nil && capturedInput.Normalizer == input.Normalizer && (capturedInput.SourceError == nil) == (input.SourceError == nil)
	if input.SourceError == nil && input.OptionError == nil {
		valid = valid && key.current(capture.view, d, capturedAuth.Access().State.CurrentUser, path)
	}
	key.runtime = nil
	if !valid {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, ids.ErrCatalogReplaced
	}
	h.ReleaseEngineLocked()
	runtime, err := input.Compile(ctx, &normalizerCall[A]{host: h, selected: input.Normalizer, capture: capture})
	if err != nil {
		if ctx.Err() == nil {
			h.ReacquireEngine()
			currentView := h.PrivateCatalogLocked(changes)
			currentAuth, authErr := h.AuthorizationLocked()
			if authErr != nil {
				h.ReleaseEngineLocked()
				return dictionaryBinding{}, authErr
			}
			currentAuth = currentAuth.Recaptured(currentView)
			valid := capture.currentLocked(currentView, currentAuth) && currentView.Get(address) == d
			if input.Normalizer != nil {
				valid = valid && currentView.Get(input.Normalizer.Address()) == input.Normalizer
			}
			if input.Template != nil {
				valid = valid && currentView.Get(input.Template.Address()) == input.Template && currentView.Get(input.Template.Init) == key.init && currentView.Get(input.Template.Lexize) == key.lexize
			}
			h.ReleaseEngineLocked()
			if !valid {
				return dictionaryBinding{}, ids.ErrCatalogReplaced
			}
		}
		return dictionaryBinding{}, err
	}
	h.ReacquireEngine()
	currentAuth, err := h.AuthorizationLocked()
	if err != nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, err
	}
	currentView := h.PrivateCatalogLocked(changes)
	currentAuth = currentAuth.Recaptured(currentView)
	key.runtime = runtime
	if !capture.currentLocked(currentView, currentAuth) || currentView.Get(address) != d || !key.current(currentView, d, currentAuth.Access().State.CurrentUser, path) {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, ids.ErrCatalogReplaced
	}
	current, err := extension.CaptureThesaurusInput(h.DictionaryHost(), currentAuth, template, d.Options)
	if err != nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, err
	}
	if current == nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, ids.ErrCatalogReplaced
	}
	if current.SourceError != nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, current.SourceError
	}
	currentPath, err := currentAuth.ReferencePath()
	if err != nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, err
	}
	if err = ctx.Err(); err != nil {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, err
	}
	if current.Normalizer != input.Normalizer || !input.Current(currentView) || !key.current(currentView, d, currentAuth.Access().State.CurrentUser, currentPath) || currentView.Get(address) != d {
		h.ReleaseEngineLocked()
		return dictionaryBinding{}, ids.ErrCatalogReplaced
	}
	if capture.memos == nil {
		capture.memos = make(map[ids.ObjectAddress]dictionaryMemo)
	}
	capture.memos[address] = key
	h.ReleaseEngineLocked()
	return bindDictionary(currentView, d, runtime, input.Normalizer)
}
