package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typecmds"
)

// captureDumpLocked reads the catalog, roles, storage and sequence state Dump
// needs and runs the Collect tables (control.Collect) over it. The caller
// holds Engine.mu and releases it once on return.
func captureDumpLocked(e *core.Engine, ctx context.Context) (*control.Capture, error) {
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
	if err := typecmds.ValidateTemporaryCollationDump(e, ctx, catalogView); err != nil {
		return nil, err
	}
	if !foreignNamespacesCurrent(&e.Foreign, state) {
		return nil, fmt.Errorf("stuntgres: dump cannot serialize a renamed or retired configured foreign namespace")
	}
	capture, err := control.Collect(ctx, control.Source{
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
	if err != nil {
		return nil, err
	}
	capture.ExtendedStatistics, err = storage.CaptureExtendedStatisticsSnapshots(ctx, originalCatalog, e.Storage.RelationMetadata, core.StatisticsSnapshotCodec(ctx, e, originalCatalog))
	if err != nil {
		return nil, err
	}
	capture.AttributeStatistics, err = storage.CaptureAttributeStatisticsSnapshots(ctx, originalCatalog, e.Storage.RelationMetadata, core.StatisticsSnapshotCodec(ctx, e, originalCatalog))
	if err != nil { return nil, err }
	capture.RelationStatistics, err = storage.CaptureRelationStatisticsSnapshots(ctx, originalCatalog, e.Storage.RelationMetadata)
	if err != nil { return nil, err }
	capture.InitialPrivileges, err = control.CaptureInitialPrivileges(ctx, originalCatalog, originalRoles)
	if err != nil {
		return nil, err
	}
	return capture, nil
}

// foreignNamespacesCurrent reports whether every namespace the foreign
// registry configured still exists in state under its configured name.
func foreignNamespacesCurrent(r *fdw.Registry, state *catalog.State) bool {
	for address, name := range r.NamespaceEntries() {
		ns, ok := state.Namespace(address)
		if !ok || ns.Name() != name {
			return false
		}
	}
	return true
}
