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
// view and statement-owned catalog stage. ExtensionSlot retains the host's
// captured namespace and incumbent-prerequisite observations for publication.
// Every method runs with Engine.Mu held; the wrapped CatalogStage retains its
// lifecycle checks for writes.
type extensionHost struct {
	Engine        *core.Engine
	Op            *session.Operation
	Auth          core.AuthorizationView
	Stage         *core.CatalogStage
	namespaces    []extensionNamespace
	prerequisites []extensionPrerequisite
	// Membership and relocation, including same-target admission, retain the
	// extension predecessor even when no descriptor is replaced.
	predecessor *catalog.Extension
}

type extensionNamespace struct {
	Address     ids.ObjectAddress
	Predecessor catalog.Descriptor
}

type extensionPrerequisite struct {
	descriptor  *catalog.Extension
	predecessor catalog.Descriptor
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

// extensionNamespacesOf returns the captured schemas, including committed
// absence for a transaction-private schema. A foreign stage has no host inputs.
func extensionNamespacesOf(stage *core.CatalogStage) []extensionNamespace {
	if stage.ExtensionSlot == nil {
		return nil
	}
	h, ok := stage.ExtensionSlot.(*extensionHost)
	if !ok {
		panic(fmt.Sprintf("stuntgres: unexpected extension slot %T", stage.ExtensionSlot))
	}
	return h.namespaces
}

func (h *extensionHost) Context() context.Context { return h.Op.Context() }
func (h *extensionHost) Notice(n sqlstate.Error)  { h.Op.EmitNotice(session.LevelNotice, n) }

// ActivatePgTrgm runs after the first setter's duplicate check, at its C validator.
// Engine.Mu is held; the core helper releases settings/session locks before
// these owned warnings enter the ordinary operation queue (no user callback).
func (h *extensionHost) ActivatePgTrgm() error {
	warnings, err := h.Engine.ActivatePgTrgmLocked(h.Op)
	for _, warning := range warnings {
		h.Op.EmitNotice(session.LevelWarning, *warning)
	}
	return err
}

func (h *extensionHost) InitializePgcrypto(scriptContext string) error {
	return h.Engine.InitializePgcryptoLocked(h.Op, scriptContext)
}
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
	h.Stage = stage
	stage.ExtensionSlot = h
	h.ObserveNamespace(namespace)
	return h
}

func (h *extensionHost) committedPredecessor(address ids.ObjectAddress) catalog.Descriptor {
	if h.Auth.Statement != nil {
		if journal := h.Auth.Statement.PendingCatalog(); journal != nil {
			if predecessor, exists := journal.Originals[address]; exists {
				return predecessor
			}
		}
	}
	return h.Engine.Catalog.State.Get(address)
}

func (h *extensionHost) ObserveNamespace(address ids.ObjectAddress) {
	for _, observed := range h.namespaces {
		if observed.Address == address {
			return
		}
	}
	h.namespaces = append(h.namespaces, extensionNamespace{Address: address, Predecessor: h.committedPredecessor(address)})
}

func (h *extensionHost) ObservePrerequisite(address ids.ObjectAddress) {
	descriptor, ok := h.Stage.Base.Extension(address)
	if !ok {
		return
	} // A new closure member is already fenced by CREATE.
	for _, observed := range h.prerequisites {
		if observed.descriptor.Address() == address {
			return
		}
	}
	h.prerequisites = append(h.prerequisites, extensionPrerequisite{descriptor: descriptor, predecessor: h.committedPredecessor(address)})
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

func (h *extensionHost) CreationRoutineACL(owner authorization.PrincipalRef, namespace ids.ObjectAddress) authorization.ACLValue {
	return h.Stage.CreationACL(owner, namespace, authorization.DefaultRoutines)
}

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
func (h *extensionHost) ScriptFunctions(owner authorization.PrincipalRef, ext *catalog.Extension, helpers []extension.ScriptFunction) ([]*catalog.Procedure, error) {
	e, auth := h.Engine, h.Auth
	auth.Catalog = e.StageRead(h.Stage)
	auth.Recapture(auth.Catalog)
	auth.State.CurrentUser = owner
	required, err := catalog.ExtensionPrerequisites(auth.Catalog, ext)
	if err != nil {
		return nil, err
	}
	schemas := make([]ids.ObjectAddress, 1, 1+len(required))
	schemas[0] = ext.TargetNamespace
	for _, address := range required {
		prerequisite, _ := auth.Catalog.Extension(address)
		namespace := prerequisite.TargetNamespace
		if namespace.ObjectID == ids.PGCatalogNamespaceOID {
			continue
		}
		duplicate := false
		for _, previous := range schemas {
			if namespace == previous {
				duplicate = true
				break
			}
		}
		if !duplicate {
			schemas = append(schemas, namespace)
		}
	}
	for _, namespace := range schemas {
		if err := auth.CanUse(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), namespace)); err != nil {
			return nil, err
		}
	}
	// EffectivePath retains implicit pg_catalog priority and excludes TEMP
	// procedures, even when the caller supplied an explicit TEMP entry.
	path, err := core.EffectivePath(auth, schemas, analyze.InstalledFunctionLookup)
	if err != nil {
		return nil, err
	}
	found := make([]*catalog.Procedure, len(helpers))
	for i, helper := range helpers {
		found[i] = analyze.ProcedureInNamespaces(auth.Catalog, analyze.NamespaceSelection{Path: path}, helper.Name, helper.Signature)
	}
	return found, nil
}
