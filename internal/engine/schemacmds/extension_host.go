package schemacmds

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// extensionHost is the engine side of the extension handlers and their
// staging: the engine, the statement's operation and captured authorization
// view, and the catalog stage and its namespace once BeginStage has begun it.
// BeginStage stores the host in the stage's ExtensionSlot, so publication
// recovers the namespace (extensionNamespaceOf). Every method runs with e.mu
// held (extension.Host and extension.Stage contracts). It wraps the stage
// rather than being a defined type over CatalogStage, so Create and Replace
// keep CatalogStage's lifecycle checks.
type extensionHost struct {
	Engine    *core.Engine
	Op        *session.Operation
	Auth      core.AuthorizationView
	Stage     *core.CatalogStage
	namespace ids.ObjectAddress
	// Membership and relocation, including same-target admission, retain the
	// extension predecessor even when no descriptor is replaced.
	predecessor *catalog.Extension
}

var (
	_ extension.Host  = (*extensionHost)(nil)
	_ extension.Stage = (*extensionHost)(nil)
	_ ddl.Authority   = core.AuthorizationView{}
)

// stageOf returns the root stage behind a Stage that BeginStage returned.
func stageOf(s extension.Stage) (*core.CatalogStage, error) {
	h, ok := s.(*extensionHost)
	if !ok || h == nil || h.Stage == nil {
		return nil, fmt.Errorf("stuntgres: foreign extension stage")
	}
	return h.Stage, nil
}

// extensionNamespaceOf returns the namespace stage was begun in; a stage no
// extension host began has the zero address.
func extensionNamespaceOf(stage *core.CatalogStage) ids.ObjectAddress {
	if stage.ExtensionSlot == nil {
		return ids.ObjectAddress{}
	}
	h, ok := stage.ExtensionSlot.(*extensionHost)
	if !ok {
		panic(fmt.Sprintf("stuntgres: unexpected extension slot %T", stage.ExtensionSlot))
	}
	return h.namespace
}

func (h *extensionHost) Context() context.Context { return h.Op.Context() }
func (h *extensionHost) Notice(n sqlstate.Error)  { h.Op.EmitNotice(session.LevelNotice, n) }
func (h *extensionHost) CreationPath() ([]ids.ObjectAddress, error) {
	path, _, err := core.ExplicitPath(h.Auth)
	return path, err
}
func (h *extensionHost) ReplayingTableDestination() bool {
	return core.OperationReplayOrZero(h.Op).TableDestination
}
func (h *extensionHost) BootstrapSuperuser() authorization.PrincipalRef {
	return h.Engine.RoleStore.Domain.Principal(10)
}
func (h *extensionHost) BeginStage(namespace ids.ObjectAddress) extension.Stage {
	stage := h.Engine.BeginCatalogStage(h.Auth.Catalog)
	stage.AuthorizeLocked(h.Auth, h.Op.Session)
	h.Stage, h.namespace = stage, namespace
	stage.ExtensionSlot = h
	return h
}

func (h *extensionHost) Read() catalog.Read                         { return h.Stage.Base }
func (h *extensionHost) CurrentRead() catalog.Read                  { return h.Engine.StageRead(h.Stage) }
func (h *extensionHost) Get(a ids.ObjectAddress) catalog.Descriptor { return h.Stage.Get(a) }
func (h *extensionHost) LookupName(key ids.NameKey) ids.ObjectAddress {
	return h.Stage.LookupName(key)
}
func (h *extensionHost) Create(d catalog.Descriptor) error { return h.Stage.Create(d) }
func (h *extensionHost) Replace(d catalog.Descriptor) error {
	// Sealing a script and admitting its generated companions revise objects
	// already created in this candidate, not predecessors in the base catalog.
	// Keep their CREATE name/absence observations and canonical snapshots intact.
	if d != nil && h.Stage != nil && h.Stage.Lifecycle == core.CatalogOpen && h.Stage.Invalid == nil && h.Stage.Base.Get(d.Address()) == nil {
		if previous := h.Stage.Get(d.Address()); previous != nil && catalog.DescriptorNameKey(previous) == catalog.DescriptorNameKey(d) {
			h.Stage.UpdateDescriptor(d)
			return nil
		}
	}
	return h.Stage.Replace(d)
}
func (h *extensionHost) RecordInitialPrivilege(address ids.ObjectAddress, kind byte, acl authorization.ACLValue) {
	h.Stage.RecordInitialPrivilege(address, kind, acl)
}

func (h *extensionHost) Roles() authorization.Read { return h.Auth.Roles }

func (h *extensionHost) RoutineCreationACLWithoutPublic(owner authorization.PrincipalRef, namespace ids.ObjectAddress) (authorization.ACLValue, error) {
	if h.Stage == nil || h.Stage.Lifecycle != core.CatalogOpen || h.Stage.Invalid != nil {
		return authorization.ACLValue{}, fmt.Errorf("stuntgres: invalid catalog stage")
	}
	acl := h.Stage.CreationACL(owner, namespace, authorization.DefaultRoutines)
	if !acl.Explicit {
		acl = authorization.DefaultBaseline(owner, authorization.DefaultRoutines, false)
	}
	acl, _, err := authorization.ChangeObjectACL(h.Op.Context(), h.Auth.Roles, acl, owner, owner,
		[]authorization.ACLGrantee{{Public: true}}, authorization.ExecutePrivilege, false, true, false, false)
	return acl, err
}
func (h *extensionHost) AllocateOID() (ids.OID, error) { return h.Engine.Catalog.AllocateOIDLocked() }
func (h *extensionHost) Revision() (ids.DefinitionRevision, error) {
	return h.Engine.Catalog.RevisionLocked()
}
func (h *extensionHost) DisplaceAssociatedArray(namespace ids.ObjectAddress, name string) (bool, error) {
	return h.Engine.DisplaceAssociatedArrayLocked(h.Stage, namespace, name)
}
func (h *extensionHost) AssociatedArrayName(namespace ids.ObjectAddress, name string) string {
	return core.AssociatedArrayName(h.Stage, namespace, name)
}
func (h *extensionHost) CreationTypeACL(owner authorization.PrincipalRef, namespace ids.ObjectAddress) (authorization.ACLValue, bool) {
	return h.Stage.CreationTypeACL(owner, namespace)
}

// ScriptFunctions resolves helper names against the coherent staged search
// path, under the actual script execution principal. In particular,
// unqualified function lookup excludes the installer's own TEMP namespace.
func (h *extensionHost) ScriptFunctions(owner authorization.PrincipalRef, namespace ids.ObjectAddress, helpers []extension.ScriptFunction) ([]*catalog.Procedure, error) {
	e, auth := h.Engine, h.Auth
	auth.Catalog = e.StageRead(h.Stage)
	auth.Recapture(auth.Catalog)
	auth.State.CurrentUser = owner
	if err := auth.CanUse(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), namespace)); err != nil {
		return nil, err
	}
	// Extension scripts replace, rather than prefix, the caller's path. The
	// stock scripts have no prerequisite schemas; implicit pg_catalog remains,
	// and the script's explicit pg_temp entry is excluded from function lookup.
	path := core.EffectivePath(auth, []ids.ObjectAddress{namespace}, analyze.InstalledFunctionLookup)
	found := make([]*catalog.Procedure, len(helpers))
	for i, helper := range helpers {
		found[i] = analyze.ProcedureInNamespaces(auth.Catalog, analyze.NamespaceSelection{Path: path}, helper.Name, helper.Signature)
	}
	return found, nil
}
