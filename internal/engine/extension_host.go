package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// extensionHost is the engine side of the extension handlers and their
// staging: the engine, the statement's operation and captured authorization
// view, and the catalog stage once BeginStage has begun it. Every method runs
// with e.mu held (extension.Host and extension.Stage contracts). It wraps the
// stage rather than being a defined type over CatalogStage, so Create and
// Replace keep CatalogStage's lifecycle checks.
type extensionHost struct {
	engine *Engine
	op     *session.Operation
	auth   AuthorizationView
	stage  *CatalogStage
}

var (
	_ extension.Host  = (*extensionHost)(nil)
	_ extension.Stage = (*extensionHost)(nil)
	_ ddl.Authority   = AuthorizationView{}
)

// stageOf returns the root stage behind a Stage that BeginStage returned.
func stageOf(s extension.Stage) (*CatalogStage, error) {
	h, ok := s.(*extensionHost)
	if !ok || h == nil || h.stage == nil {
		return nil, fmt.Errorf("stuntgres: foreign extension stage")
	}
	return h.stage, nil
}

func (h *extensionHost) Context() context.Context { return h.op.Context() }
func (h *extensionHost) Notice(n sqlstate.Error)  { h.op.EmitNotice(session.LevelNotice, n) }
func (h *extensionHost) CreationPath() ([]ids.ObjectAddress, error) {
	path, _, err := explicitPath(h.auth)
	return path, err
}
func (h *extensionHost) ReplayingTableDestination() bool {
	return operationReplayOrZero(h.op).tableDestination
}
func (h *extensionHost) BootstrapSuperuser() authorization.PrincipalRef {
	return h.engine.roles.Domain.Principal(10)
}
func (h *extensionHost) BeginStage(namespace ids.ObjectAddress) extension.Stage {
	stage := h.engine.BeginCatalogStage(h.auth.Catalog)
	stage.authorizeLocked(h.auth, h.op.Session)
	stage.extensionNamespace = namespace
	h.stage = stage
	return h
}

func (h *extensionHost) Read() catalog.Read { return h.stage.Base }
func (h *extensionHost) LookupName(key ids.NameKey) ids.ObjectAddress {
	return h.stage.LookupName(key)
}
func (h *extensionHost) Create(d catalog.Descriptor) error  { return h.stage.Create(d) }
func (h *extensionHost) Replace(d catalog.Descriptor) error { return h.stage.Replace(d) }
func (h *extensionHost) AllocateOID() (ids.OID, error)      { return h.engine.catalog.AllocateOIDLocked() }
func (h *extensionHost) Revision() (ids.DefinitionRevision, error) {
	return h.engine.catalog.RevisionLocked()
}
func (h *extensionHost) DisplaceAssociatedArray(namespace ids.ObjectAddress, name string) (bool, error) {
	return h.engine.displaceAssociatedArrayLocked(h.stage, namespace, name)
}
func (h *extensionHost) AssociatedArrayName(namespace ids.ObjectAddress, name string) string {
	return associatedArrayName(h.stage, namespace, name)
}
func (h *extensionHost) CreationTypeACL(owner authorization.PrincipalRef, namespace ids.ObjectAddress) (authorization.ACLValue, bool) {
	return h.stage.CreationTypeACL(owner, namespace)
}

// ScriptFunctions resolves helper names against the coherent staged search
// path, under the actual script execution principal. In particular,
// unqualified function lookup excludes the installer's own TEMP namespace.
func (h *extensionHost) ScriptFunctions(owner authorization.PrincipalRef, namespace ids.ObjectAddress, helpers []extension.ScriptFunction) ([]*catalog.Procedure, error) {
	e, auth := h.engine, h.auth
	auth.Catalog = e.stageRead(h.stage)
	auth.Recapture(auth.Catalog)
	auth.State.CurrentUser = owner
	if err := auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), namespace)); err != nil {
		return nil, err
	}
	// Extension scripts replace, rather than prefix, the caller's path. The
	// stock scripts have no prerequisite schemas; implicit pg_catalog remains,
	// and the script's explicit pg_temp entry is excluded from function lookup.
	path := effectivePath(auth, []ids.ObjectAddress{namespace}, analyze.InstalledFunctionLookup)
	found := make([]*catalog.Procedure, len(helpers))
	for i, helper := range helpers {
		found[i] = analyze.ProcedureInNamespaces(auth.Catalog, analyze.NamespaceSelection{Path: path}, helper.Name, helper.Signature)
	}
	return found, nil
}
