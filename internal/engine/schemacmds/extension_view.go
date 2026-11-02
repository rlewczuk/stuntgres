package schemacmds

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type scriptViewHost struct{ tablecmds.TableHost }

func (h scriptViewHost) TemporaryOwner(auth ddl.Authority) ids.SessionToken {
	return core.CatalogSourceTemporaryOwner(core.DDLAuthorityOf(auth))
}
func (h scriptViewHost) ReownDisplacedTypes() error {
	return h.E.ReownStagedTypeGenerationsLocked(h.CatalogStage)
}

var _ view.CreateHost = scriptViewHost{}

// ScriptView holds Engine.Mu and uses the installer's existing unpublished
// candidate. It neither executes SQL recursively nor publishes an interim view.
func (h *extensionHost) ScriptView(owner authorization.PrincipalRef, namespace ids.ObjectAddress, declaration *parser.CreateView) (*catalog.Relation, error) {
	e := h.Engine
	auth := h.Auth
	auth.Catalog = e.StageRead(h.Stage)
	auth.Recapture(auth.Catalog)
	auth.State.CurrentUser = owner
	auth = auth.WithScriptNamespace(namespace)
	ns := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), namespace)
	if err := auth.CanUse(ns); err != nil {
		return nil, err
	}
	if err := auth.CheckCreate(ns); err != nil {
		return nil, err
	}
	// The manifest is owned by this call. Qualify only its destination; body
	// lookup uses the same ordinary namespace authority as all other bindings.
	declaration.Name = parser.ObjectName{Parts: []parser.Identifier{{Text: auth.Catalog.MustNamespace(namespace).Name()}, declaration.Name.Parts[len(declaration.Name.Parts)-1]}}
	old, err := view.CreateTarget(auth.View, auth, declaration, ns, ids.ObjectRef{})
	if err != nil {
		return nil, err
	}
	b, err := core.Describe(h.Op.Context(), e.AnalysisHost(h.Op), declaration, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, nil, nil)
	if err != nil {
		return nil, err
	}
	options, err := view.CheckCreate(auth.Catalog, declaration, b, old)
	if err != nil {
		return nil, err
	}
	host := scriptViewHost{tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: h.Op, CatalogStage: h.Stage}}}
	if err := view.StageCreate(host, auth, declaration, old, ns, b, options, false); err != nil {
		return nil, err
	}
	address := h.Stage.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: declaration.Name.Parts[len(declaration.Name.Parts)-1].Text})
	return e.StageRead(h.Stage).MustRelation(address), nil
}
