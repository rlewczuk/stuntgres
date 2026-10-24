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
	state := e.catalog.State
	if err := control.CheckPortable(state); err != nil {
		return nil, err
	}
	catalogView := e.catalog.Read(state)
	originalCatalog := catalogView
	originalRoles := e.roles.read(&e.catalog.Owner, originalCatalog, e.roles.published)
	recipePlan, err := table.CapturedGenerationReplayPlan(originalCatalog)
	if err != nil {
		return nil, err
	}
	aclOutput := regobj.ACLItemOutput{Owner: &e.catalog.Owner, Roles: originalRoles, Lossless: true}
	ctx = regobj.ACLItemOutputContext(ctx, aclOutput)
	if err := control.PreflightACLItems(ctx, originalCatalog, e.storage.Relations, e.visibility.CommitSeq(), aclOutput); err != nil {
		return nil, err
	}
	// Declarations bind private key names until the terminal renames. Later
	// definitions and values must render against the original captured names.
	catalogView, constraintReplay := partition.ConstraintDumpCatalog(control.Resolver{}, catalogView)
	domainRoles := e.roles.read(&e.catalog.Owner, catalogView, e.roles.published)
	if err := e.validateTemporaryCollationDump(ctx, catalogView); err != nil {
		return nil, err
	}
	for address, name := range e.foreign.NamespaceEntries() {
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
		Relations:        e.storage.Relations,
		Sequences:        e.storage.Sequences,
		Frontier:         e.visibility.CommitSeq(),
		Bootstrap:        e.roles.domain.Principal(authorization.BootstrapPrincipalID),
		Foreign:          e.foreign,
	})
}
