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
type thesaurusMemo struct {
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
func (m thesaurusMemo) current(view catalog.Read, d *catalog.TSDictionary, role authorization.PrincipalRef, path []ids.ObjectAddress) bool {
	if m.runtime == nil || m.dictionary != d || m.domain != view.Domain() || m.epoch != view.Epoch() || m.role != role || !slices.Equal(m.path, path) {
		return false
	}
	if view.Get(d.Template) != m.thesTemplate || view.Get(m.thesTemplate.Init) != m.thesInit || view.Get(m.thesTemplate.Lexize) != m.thesLexize || view.Get(m.normalizer.Address()) != m.normalizer || view.Get(m.normalizer.Template) != m.template || view.Get(m.template.Init) != m.init || view.Get(m.template.Lexize) != m.lexize {
		return false
	}
	namespace, ok := view.Namespace(m.normalizer.Namespace())
	return ok && namespace.Name() == m.normalizerNamespaceName && view.NameAddress(catalog.DescriptorNameKey(m.normalizer)) == m.normalizer.Address()
}

// acquireDictionary runs resource parsing and callbacks outside Engine.Mu.
// Only successful session initialization is retained; errors never install state
// or permit a stale initialized runtime after executable inputs have changed.
func acquireDictionary[A Authority[A]](h Host[A], address ids.ObjectAddress) (catalog.DictionaryRuntime, error) {
	return acquireDictionaryFor(h, address, nil)
}

func acquireDictionaryFor[A Authority[A]](h Host[A], address ids.ObjectAddress, capture *tsAcquisition[A]) (catalog.DictionaryRuntime, error) {
	payload := h.ThesaurusMemos()
	ctx := h.Operation().Context()
	if capture != nil {
		ctx = capture.ctx
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h.ReacquireEngine()
		auth, err := h.AuthorizationLocked()
		if err != nil {
			h.ReleaseEngineLocked()
			return nil, err
		}
		changes := h.PendingCatalog()
		view := h.PrivateCatalogLocked(changes)
		auth = auth.Recaptured(view)
		if capture != nil && (!capture.currentLocked(view, auth) || view.Get(address) != capture.view.Get(address)) {
			h.ReleaseEngineLocked()
			return nil, ids.ErrCatalogReplaced
		}
		d, ok := view.TSDictionary(address)
		if !ok {
			h.ReleaseEngineLocked()
			return nil, sqlError("XX000", fmt.Sprintf("cache lookup failed for text search dictionary %d", address.ObjectID))
		}
		if err = (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, d); err != nil {
			h.ReleaseEngineLocked()
			return nil, err
		}
		if !extension.IsThesaurus(view, d) {
			h.ReleaseEngineLocked()
			return d.Runtime, nil
		}
		path, err := auth.ReferencePath()
		if err != nil {
			h.ReleaseEngineLocked()
			return nil, err
		}
		for a, memo := range *payload {
			if view.Get(a) != memo.dictionary || memo.domain != view.Domain() || memo.epoch != view.Epoch() {
				delete(*payload, a)
			}
		}
		if memo := (*payload)[address]; memo.current(view, d, auth.Access().State.CurrentUser, path) {
			if capture != nil {
				if !memo.current(capture.view, d, auth.Access().State.CurrentUser, path) {
					h.ReleaseEngineLocked()
					return nil, ids.ErrCatalogReplaced
				}
				if capture.memos == nil {
					capture.memos = make(map[ids.ObjectAddress]thesaurusMemo)
				}
				capture.memos[address] = memo
			}
			h.ReleaseEngineLocked()
			return memo.runtime, ctx.Err()
		}
		template, _ := view.TSTemplate(d.Template)
		input, err := extension.CaptureThesaurusInput(h.DictionaryHost(), auth, template, d.Options)
		if err != nil {
			h.ReleaseEngineLocked()
			return nil, err
		}
		key := thesaurusMemo{dictionary: d, normalizer: input.Normalizer, template: input.Template, thesTemplate: template, thesInit: view.Get(template.Init), thesLexize: view.Get(template.Lexize), role: auth.Access().State.CurrentUser, path: slices.Clone(path), domain: view.Domain(), epoch: view.Epoch()}
		if input.Template != nil {
			key.init, key.lexize = view.Get(input.Template.Init), view.Get(input.Template.Lexize)
		}
		if input.Normalizer != nil {
			key.normalizerNamespaceName = view.MustNamespace(input.Normalizer.Namespace()).Name()
		}
		if capture != nil {
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
				return nil, ids.ErrCatalogReplaced
			}
		}
		h.ReleaseEngineLocked()
		runtime, err := input.Compile(ctx)
		if err != nil {
			if capture != nil && ctx.Err() == nil {
				h.ReacquireEngine()
				currentView := h.PrivateCatalogLocked(changes)
				currentAuth, authErr := h.AuthorizationLocked()
				if authErr != nil {
					h.ReleaseEngineLocked()
					return nil, authErr
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
					return nil, ids.ErrCatalogReplaced
				}
			}
			return nil, err
		}
		h.ReacquireEngine()
		currentAuth, err := h.AuthorizationLocked()
		if err != nil {
			h.ReleaseEngineLocked()
			return nil, err
		}
		currentView := h.PrivateCatalogLocked(changes)
		currentAuth = currentAuth.Recaptured(currentView)
		key.runtime = runtime
		if capture != nil && (!capture.currentLocked(currentView, currentAuth) || currentView.Get(address) != d || !key.current(currentView, d, currentAuth.Access().State.CurrentUser, path)) {
			h.ReleaseEngineLocked()
			return nil, ids.ErrCatalogReplaced
		}
		current, err := extension.CaptureThesaurusInput(h.DictionaryHost(), currentAuth, template, d.Options)
		if err != nil {
			h.ReleaseEngineLocked()
			return nil, err
		}
		if current == nil {
			h.ReleaseEngineLocked()
			continue
		}
		if current.SourceError != nil {
			h.ReleaseEngineLocked()
			return nil, current.SourceError
		}
		currentPath, err := currentAuth.ReferencePath()
		if err != nil {
			h.ReleaseEngineLocked()
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			h.ReleaseEngineLocked()
			return nil, err
		}
		if current.Normalizer != input.Normalizer || !input.Current(currentView) || !key.current(currentView, d, currentAuth.Access().State.CurrentUser, currentPath) || currentView.Get(address) != d {
			h.ReleaseEngineLocked()
			if capture != nil {
				return nil, ids.ErrCatalogReplaced
			}
			continue
		}
		if capture != nil {
			if capture.memos == nil {
				capture.memos = make(map[ids.ObjectAddress]thesaurusMemo)
			}
			capture.memos[address] = key
			h.ReleaseEngineLocked()
			return runtime, nil
		}
		if *payload == nil {
			*payload = make(ThesaurusMemos)
		}
		(*payload)[address] = key
		h.ReleaseEngineLocked()
		return runtime, nil
	}
}
