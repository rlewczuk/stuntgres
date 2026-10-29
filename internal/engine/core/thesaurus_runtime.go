package core

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
func (m thesaurusMemo) current(view catalog.Read, d *catalog.TSDictionary, auth AuthorizationView, path []ids.ObjectAddress) bool {
	if m.runtime == nil || m.dictionary != d || m.domain != view.Domain() || m.epoch != view.Epoch() || m.role != auth.State.CurrentUser || !slices.Equal(m.path, path) {
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
func (h *statementHost) acquireDictionary(address ids.ObjectAddress) (catalog.DictionaryRuntime, error) {
	return h.acquireDictionaryFor(address, nil)
}

func (h *statementHost) acquireDictionaryFor(address ids.ObjectAddress, capture *tsAcquisition) (catalog.DictionaryRuntime, error) {
	s := h.scope()
	e := s.Engine()
	payload := PayloadOf(s.Op.Session)
	ctx := s.Op.Context()
	if capture != nil {
		ctx = capture.ctx
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(s.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		changes := s.Catalog
		if changes == nil {
			changes = s.Owner.Catalog
		}
		view := e.PrivateCatalogLocked(changes)
		auth.Recapture(view)
		if capture != nil && (!capture.currentLocked(view, auth) || view.Get(address) != capture.view.Get(address)) {
			e.Mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
		d, ok := view.TSDictionary(address)
		if !ok {
			e.Mu.Unlock()
			return nil, SQLError("XX000", fmt.Sprintf("cache lookup failed for text search dictionary %d", address.ObjectID))
		}
		if err = (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, d); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		if !extension.IsThesaurus(view, d) {
			e.Mu.Unlock()
			return d.Runtime, nil
		}
		path, err := auth.ReferencePath()
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		for a, memo := range payload.thesaurusMemos {
			if view.Get(a) != memo.dictionary || memo.domain != view.Domain() || memo.epoch != view.Epoch() {
				delete(payload.thesaurusMemos, a)
			}
		}
		if memo := payload.thesaurusMemos[address]; memo.current(view, d, auth, path) {
			if capture != nil {
				if !memo.current(capture.view, d, auth, path) {
					e.Mu.Unlock()
					return nil, ids.ErrCatalogReplaced
				}
				if capture.memos == nil {
					capture.memos = make(map[ids.ObjectAddress]thesaurusMemo)
				}
				capture.memos[address] = memo
			}
			e.Mu.Unlock()
			return memo.runtime, ctx.Err()
		}
		template, _ := view.TSTemplate(d.Template)
		input, err := extension.CaptureThesaurusInput(DDLHost{E: e, Op: s.Op}, auth, template, d.Options)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		key := thesaurusMemo{dictionary: d, normalizer: input.Normalizer, template: input.Template, thesTemplate: template, thesInit: view.Get(template.Init), thesLexize: view.Get(template.Lexize), role: auth.State.CurrentUser, path: slices.Clone(path), domain: view.Domain(), epoch: view.Epoch()}
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
			capturedAuth := auth
			capturedAuth.Recapture(capture.view)
			capturedInput, captureErr := extension.CaptureThesaurusInput(DDLHost{E: e, Op: s.Op}, capturedAuth, template, d.Options)
			key.runtime = d.Runtime
			valid := captureErr == nil && capturedInput != nil && capturedInput.Normalizer == input.Normalizer && (capturedInput.SourceError == nil) == (input.SourceError == nil)
			if input.SourceError == nil && input.OptionError == nil {
				valid = valid && key.current(capture.view, d, capturedAuth, path)
			}
			key.runtime = nil
			if !valid {
				e.Mu.Unlock()
				return nil, ids.ErrCatalogReplaced
			}
		}
		e.Mu.Unlock()
		runtime, err := input.Compile(ctx)
		if err != nil {
			if capture != nil && ctx.Err() == nil {
				e.Mu.Lock()
				currentView := e.PrivateCatalogLocked(changes)
				currentAuth, authErr := e.AuthorizationLocked(s.Op)
				if authErr != nil {
					e.Mu.Unlock()
					return nil, authErr
				}
				currentAuth.Recapture(currentView)
				valid := capture.currentLocked(currentView, currentAuth) && currentView.Get(address) == d
				if input.Normalizer != nil {
					valid = valid && currentView.Get(input.Normalizer.Address()) == input.Normalizer
				}
				if input.Template != nil {
					valid = valid && currentView.Get(input.Template.Address()) == input.Template && currentView.Get(input.Template.Init) == key.init && currentView.Get(input.Template.Lexize) == key.lexize
				}
				e.Mu.Unlock()
				if !valid {
					return nil, ids.ErrCatalogReplaced
				}
			}
			return nil, err
		}
		e.Mu.Lock()
		currentAuth, err := e.AuthorizationLocked(s.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		currentView := e.PrivateCatalogLocked(changes)
		currentAuth.Recapture(currentView)
		key.runtime = runtime
		if capture != nil && (!capture.currentLocked(currentView, currentAuth) || currentView.Get(address) != d || !key.current(currentView, d, currentAuth, path)) {
			e.Mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
		current, err := extension.CaptureThesaurusInput(DDLHost{E: e, Op: s.Op}, currentAuth, template, d.Options)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		if current == nil {
			e.Mu.Unlock()
			continue
		}
		if current.SourceError != nil {
			e.Mu.Unlock()
			return nil, current.SourceError
		}
		currentPath, err := currentAuth.ReferencePath()
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		if current.Normalizer != input.Normalizer || !input.Current(currentView) || !key.current(currentView, d, currentAuth, currentPath) || currentView.Get(address) != d {
			e.Mu.Unlock()
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
			e.Mu.Unlock()
			return runtime, nil
		}
		if payload.thesaurusMemos == nil {
			payload.thesaurusMemos = make(map[ids.ObjectAddress]thesaurusMemo)
		}
		payload.thesaurusMemos[address] = key
		e.Mu.Unlock()
		return runtime, nil
	}
}
