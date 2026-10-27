package core

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// statisticsSnapshotBindingHost supplies captured destination authority to the
// ordinary binder's type, namespace and object-name resolution hooks, using the
// existing immutable snapshot resolvers instead of live-publication admission.
// It binds only checked closed control-constructor forms, never stored
// statistics expressions or statements against a live session.
type statisticsSnapshotBindingHost struct {
	*BindingHost
	context context.Context
	auth    AuthorizationView
}

func (h *statisticsSnapshotBindingHost) BindingAuthority(base analyze.Authority) (analyze.Authority, error) {
	if base == nil {
		base = &h.auth
	}
	return h.BindingHost.BindingAuthority(base)
}

func (h *statisticsSnapshotBindingHost) CurrentCatalog() catalog.Read {
	return h.auth.Catalog
}

func (h *statisticsSnapshotBindingHost) ResolveAnnotationType(raw parser.TypeName, unknownNull bool) (sqltype.Usage, error) {
	return h.ResolveAnnotationTypeInput(raw, unknownNull, false, false, false)
}

func (h *statisticsSnapshotBindingHost) ResolveAnnotationTypeInput(raw parser.TypeName, unknownNull, domainBase, jsonReturning, thresholdArrayInput bool) (sqltype.Usage, error) {
	operation := analyze.TypeNameOperation{Context: func() context.Context { return h.context }}
	return h.Engine.resolveAnnotationTypeAuthority(raw, h.auth, unknownNull, domainBase, jsonReturning, thresholdArrayInput, operation)
}

func (h *statisticsSnapshotBindingHost) LookupNamespaces(auth analyze.Authority, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	return LookupNamespacesSnapshot(h.view(auth), name, kind)
}

func (h *statisticsSnapshotBindingHost) NamedType(auth analyze.Authority, name parser.ObjectName) (*catalog.Type, error) {
	return namedTypeSnapshot(h.view(auth), name)
}

func (h *statisticsSnapshotBindingHost) Regclass(auth analyze.Authority, text string) (ids.ObjectRef, error) {
	view := h.view(auth)
	input, err := captureRegclassInput(view)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	oid, err := input.ResolveRegclass(h.context, text)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	return view.Ref(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}), nil
}
