package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/table"
)

// captureDumpLocked reads the catalog, roles, storage and sequence state Dump
// needs and runs the Collect tables (control.Collect) over it. The caller
// holds Engine.mu and releases it once on return.
func (e *Engine) captureDumpLocked(ctx context.Context) (*control.Capture, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := e.Catalog.State
	if err := control.CheckPortable(state); err != nil {
		return nil, err
	}
	catalogView := e.Catalog.Read(state)
	originalCatalog := catalogView
	originalRoles := e.RoleStore.PublishedRead(&e.Catalog.Owner, originalCatalog)
	recipePlan, err := table.CapturedGenerationReplayPlan(originalCatalog)
	if err != nil {
		return nil, err
	}
	aclOutput := regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: originalRoles, Lossless: true}
	ctx = regobj.ACLItemOutputContext(ctx, aclOutput)
	if err := control.PreflightACLItems(ctx, originalCatalog, e.Storage.Relations, e.Visibility.CommitSeq(), aclOutput); err != nil {
		return nil, err
	}
	// Declarations bind private key names until the terminal renames. Later
	// definitions and values must render against the original captured names.
	catalogView, constraintReplay := partition.ConstraintDumpCatalog(control.Resolver{}, catalogView)
	domainRoles := e.RoleStore.PublishedRead(&e.Catalog.Owner, catalogView)
	if err := validateTemporaryCollationDump(e, ctx, catalogView); err != nil {
		return nil, err
	}
	for address, name := range e.Foreign.NamespaceEntries() {
		ns, ok := state.Namespace(address)
		if !ok || ns.Name() != name {
			return nil, fmt.Errorf("stuntgres: dump cannot serialize a renamed or retired configured foreign namespace")
		}
	}
	return control.Collect(ctx, control.Source{
		CatalogView:      catalogView,
		OriginalCatalog:  originalCatalog,
		OriginalRoles:    originalRoles,
		DomainRoles:      domainRoles,
		ConstraintReplay: constraintReplay,
		ACLOutput:        aclOutput,
		RecipePlan:       recipePlan,
		State:            state,
		Relations:        e.Storage.Relations,
		Sequences:        e.Storage.Sequences,
		Frontier:         e.Visibility.CommitSeq(),
		Bootstrap:        e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID),
		Foreign:          e.Foreign,
	})
}
