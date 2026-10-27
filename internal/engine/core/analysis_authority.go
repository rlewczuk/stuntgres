package core

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
)

// The binder holds its authority as an analyze.Authority. A stored authority
// is an *AuthorizationView; a binding authority (bindingHost.BindingAuthority)
// is a *bindingAuthorityView, the same view read through the operation's
// current definition reader. Only the host interprets the second kind.
var (
	_ analyze.Authority = (*AuthorizationView)(nil)
	_ analyze.Authority = (*bindingAuthorityView)(nil)
)

func (*AuthorizationView) AnalyzeAuthority()           {}
func (v *AuthorizationView) ReadCatalog() catalog.Read { return v.Catalog }

// bindingAuthorityView is a stored authority overlaid with the operation's
// definition reader when the host reads it. Converting the pointer costs
// nothing, so binding takes no copy of the view.
type bindingAuthorityView AuthorizationView

func (*bindingAuthorityView) AnalyzeAuthority()           {}
func (v *bindingAuthorityView) ReadCatalog() catalog.Read { return v.Catalog }
func (v *bindingAuthorityView) RuleReplica() bool         { return (*AuthorizationView)(v).RuleReplica() }

// AuthorityOf hands a copy of v to binding, as passing the view by value did.
func AuthorityOf(v AuthorizationView) analyze.Authority { return &v }

// authorizationOf recovers a stored authority outside the host. Binding
// authorities never reach engine code; meeting one is a bug.
func authorizationOf(a analyze.Authority) *AuthorizationView {
	if a == nil {
		return nil
	}
	v, ok := a.(*AuthorizationView)
	if !ok {
		panic(fmt.Sprintf("stuntgres: unexpected analysis authority %T", a))
	}
	return v
}

// authorityBase is the stored view behind a and whether a is a binding
// authority.
func authorityBase(a analyze.Authority) (*AuthorizationView, bool) {
	switch v := a.(type) {
	case *AuthorizationView:
		return v, false
	case *bindingAuthorityView:
		return (*AuthorizationView)(v), true
	}
	panic(fmt.Sprintf("stuntgres: unexpected analysis authority %T", a))
}

// operationDefinition is the operation's current definition reader, or nil.
func (h *BindingHost) operationDefinition() catalog.DefinitionReader {
	if h.Op == nil {
		return nil
	}
	return h.Op.Definition
}

// view is the authorization view behind a, with the operation's definition
// reader on a binding authority.
func (h *BindingHost) view(a analyze.Authority) AuthorizationView {
	base, binding := authorityBase(a)
	view := *base
	if d := h.operationDefinition(); binding && d != nil {
		view.Definition = d
	}
	return view
}

// BindingAuthority is the authority binding resolves under: base read through
// the operation's definition reader, or a fresh authority without one.
func (h *BindingHost) BindingAuthority(base analyze.Authority) (analyze.Authority, error) {
	if base == nil {
		auth, err := h.Engine.AuthorizationLocked(h.Op)
		if err != nil {
			return nil, err
		}
		return &auth, nil
	}
	view, binding := authorityBase(base)
	if binding || h.operationDefinition() == nil {
		return base, nil
	}
	return (*bindingAuthorityView)(view), nil
}

// BindingDefinition is the definition reader of base's binding authority: the
// operation's reader, or else base's own.
func (h *BindingHost) BindingDefinition(base analyze.Authority) catalog.DefinitionReader {
	if d := h.operationDefinition(); d != nil {
		return d
	}
	if base == nil {
		return nil
	}
	view, _ := authorityBase(base)
	return view.Definition
}

// DefinitionOf is the definition reader a reads through.
func (h *BindingHost) DefinitionOf(a analyze.Authority) catalog.DefinitionReader {
	view, binding := authorityBase(a)
	if d := h.operationDefinition(); binding && d != nil {
		return d
	}
	return view.Definition
}
