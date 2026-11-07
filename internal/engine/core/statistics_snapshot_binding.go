package core

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/stats"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// StatisticsSnapshotCodec borrows the captured catalog and ordinary control
// constructor authority. The caller owns the engine lock; no live SQL executes.
func StatisticsSnapshotCodec(ctx context.Context, e *Engine, read catalog.Read) storage.StatisticsSnapshotCodec {
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, read)
	principal := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	auth := e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: read, Catalog: read, Roles: roles, RoleRecord: e.RoleStore.Published, State: authorization.RoleState{CurrentUser: principal, Authenticated: principal}})
	acl := regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: roles, Lossless: true}
	ctx = regobj.ACLItemOutputContext(regobj.Float4DumpContext(ctx), acl)
	host := &statisticsSnapshotHost{engine: e, auth: auth, literal: exec.NewControlLiteralAuthority(auth.View)}
	host.SnapshotCodec = stats.NewSnapshotCodec(ctx, read, host)
	return host
}

// statisticsSnapshotHost is a snapshot codec's captured bootstrap authority.
// It embeds the codec it serves, so a codec is one allocation.
type statisticsSnapshotHost struct {
	stats.SnapshotCodec
	engine  *Engine
	auth    AuthorizationView
	literal exec.ControlLiteralAuthority
}

var _ stats.SnapshotHost = (*statisticsSnapshotHost)(nil)

func (h *statisticsSnapshotHost) Authority() stats.SnapshotAuthority {
	auth := h.auth
	return &auth
}

func (h *statisticsSnapshotHost) ControlLiteralAuthority() exec.ControlLiteralAuthority {
	return h.literal
}

func (h *statisticsSnapshotHost) BindingHost(ctx context.Context) analyze.Host {
	return &statisticsSnapshotBindingHost{BindingHost: h.engine.AnalysisHost(nil), context: ctx, auth: h.auth}
}

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
	return h.ResolveAnnotationTypeInput(raw, unknownNull, false, false)
}

func (h *statisticsSnapshotBindingHost) ResolveAnnotationTypeInput(raw parser.TypeName, unknownNull, domainBase, jsonReturning bool) (sqltype.Usage, error) {
	operation := analyze.TypeNameOperation{Context: func() context.Context { return h.context }}
	return h.Engine.resolveAnnotationTypeAuthority(raw, h.auth, unknownNull, domainBase, jsonReturning, operation)
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
