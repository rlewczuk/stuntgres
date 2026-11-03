package rolecmds

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
)

type databaseCreateAdmission struct {
	owner, executor authorization.PrincipalRef
	source          *core.Engine
	database        *catalog.Database
	epoch           ids.CatalogEpoch
}

func databaseCreateOwnerLocked(e *core.Engine, scope *core.StatementScope, name string) (authorization.PrincipalRef, authorization.PrincipalRef, error) {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return authorization.PrincipalRef{}, authorization.PrincipalRef{}, err
	}
	owner, err := ddl.CreateDatabaseOwner(auth.Roles, auth.State.CurrentUser, name)
	return owner, auth.State.CurrentUser, err
}

// ExecuteCreateDatabase privately copies an admitted committed source and
// registers the completed target in one publication.
func ExecuteCreateDatabase(e *core.Engine, scope *core.StatementScope, statement *parser.CreateDatabaseStmt, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	ctx := scope.Op.Context()
	options, err := ddl.ParseCreateDatabaseOptions(statement.Options)
	if err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	owner, executor, err := databaseCreateOwnerLocked(e, scope, options.Owner)
	source := e.Cluster.Targets[options.Template]
	var sourceDatabase *catalog.Database
	var sourceEpoch ids.CatalogEpoch
	if err == nil && source != nil {
		sourceDatabase = source.Catalog.State.MustDatabase(source.Catalog.DatabaseAddress())
		sourceEpoch = source.Catalog.State.Epoch()
		err = databaseTemplatePermissionLocked(e, scope, sourceDatabase)
	}
	if err == nil {
		err = ddl.CheckCreateDatabase(statement.Name, options, func(name string) bool { return e.Cluster.Targets[name] != nil })
	}
	if err == nil {
		err = ctx.Err()
	}
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if err := scope.AcquireRolePrincipal(owner, lock.AccessShare, false); err != nil {
		return core.Result{}, err
	}
	if err := scope.RoleStillPresent(owner); err != nil {
		return core.Result{}, err
	}
	if source != nil {
		// A current-source statement already owns this read reservation. Taking
		// it recursively could deadlock behind a waiting replacement writer.
		if source != e || !scope.ReplacementRead {
			source.ReplacementMu.RLock()
			defer source.ReplacementMu.RUnlock()
		}
		e.Mu.Lock()
		err = databaseTemplateCurrentLocked(e, scope, source, sourceDatabase, sourceEpoch)
		if err == nil {
			err = ddl.CheckCreateDatabase(statement.Name, options, func(name string) bool { return e.Cluster.Targets[name] != nil })
		}
		var lease *core.DatabaseTemplateLease
		if err == nil {
			lease, err = source.AcquireDatabaseTemplateLocked(scope.Op.Session)
		}
		e.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
		defer lease.Release()
	}
	admitted := databaseCreateAdmission{owner: owner, executor: executor, source: source, database: sourceDatabase, epoch: sourceEpoch}
	return createDatabaseTarget(e, ctx, scope, statement.Name, options, admitted)
}

// createDatabaseTarget constructs storage privately. Only the final no-failure
// installation below can expose a name, descriptor, target or allocator advance.
func createDatabaseTarget(e *core.Engine, ctx context.Context, scope *core.StatementScope, name string, options ddl.CreateDatabaseOptions, admitted databaseCreateAdmission) (core.Result, error) {
	owner, source := admitted.owner, admitted.source
	if source == nil {
		e.Cluster.SeedOnce.Do(func() {
			candidate, err := e.Factory(nil, "template1", e.Visibility.TracksCommitTimestamps())
			if err == nil {
				err = candidate.InstallForeignDefinitions(e.Foreign.Definitions())
			}
			if err == nil {
				err = candidate.InstallHostDefinitions(e.Catalog.HostRuntimes)
			}
			if candidate != nil {
				candidate.TextSearchFiles = e.TextSearchFiles
				candidate.IdentFile = e.IdentFile
			}
			e.Cluster.Seed, e.Cluster.SeedError = candidate, err
		})
		if e.Cluster.SeedError != nil {
			return core.Result{}, e.Cluster.SeedError
		}
		source = e.Cluster.Seed
	}
	seed := source
	e.Mu.Lock()
	target := newDatabaseRuntime(e, seed, name, owner, options)
	e.Mu.Unlock()
	if err := e.Locks.AdmitDomain(target.Catalog.Domain); err != nil {
		return core.Result{}, err
	}
	if _, err := target.ReplaceDatabaseTemplate(ctx, seed, seed.CatalogRead()); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	currentOwner, executor, err := databaseCreateOwnerLocked(e, scope, options.Owner)
	if err != nil {
		return core.Result{}, err
	}
	if currentOwner != owner || executor != admitted.executor {
		return core.Result{}, catalog.Conflict{}
	}
	if admitted.database != nil {
		if err := databaseTemplateCurrentLocked(e, scope, source, admitted.database, admitted.epoch); err != nil {
			return core.Result{}, err
		}
	} else if e.Cluster.Targets[options.Template] != nil {
		return core.Result{}, catalog.Conflict{}
	}
	scope.Op.Session.Mu.Lock()
	defer scope.Op.Session.Mu.Unlock()
	if err := core.PublicationOperationLocked(e, scope.Op); err != nil {
		return core.Result{}, err
	}
	if scope.TxRef != (ids.TxRef{}) && scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return core.Result{}, ids.ErrCatalogReplaced
	}
	result, err := publishDatabaseTargetLocked(e, ctx, name, target, owner)
	if err == nil {
		scope.CompleteUtility()
	}
	return result, err
}

func publishDatabaseTargetLocked(e *core.Engine, ctx context.Context, name string, target *core.Engine, owner authorization.PrincipalRef) (core.Result, error) {
	if e.Cluster.Targets[name] != nil {
		return core.Result{}, core.SQLError("42P04", fmt.Sprintf(`database "%s" already exists`, name))
	}
	if target == nil || target.Mu != e.Mu || target.RoleStore != e.RoleStore || target.Cluster != nil || target.DatabaseName != name || target.Catalog.Domain == e.Catalog.Domain || target.Catalog.Principals != &e.RoleStore.Domain || target.Catalog.ACLAuthority != &e.RoleStore.ACLAuthority {
		return core.Result{}, fmt.Errorf("stuntgres: foreign or already registered database target")
	}
	if _, err := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Descriptor(owner); err != nil {
		return core.Result{}, err
	}
	oid, err := catalog.DatabaseOIDLocked(e.Cluster.NextOID, &target.Catalog, e.Cluster.Catalogs)
	if err != nil {
		return core.Result{}, err
	}
	address := ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: oid}
	policy := target.Catalog.State.MustDatabase(target.Catalog.DatabaseAddress())
	database := &catalog.Database{ObjectAddress: address, ObjectRevision: 1, ObjectName: name, Owner: owner, FrozenXID: target.DatabaseFrozenXID, IsTemplate: policy.IsTemplate, AllowConnections: policy.AllowConnections, ConnectionLimit: policy.ConnectionLimit}
	targets := make(map[string]*core.Engine, len(e.Cluster.Targets)+1)
	roots := make(catalog.SharedProjection, len(e.Cluster.Targets)+1)
	for key, runtime := range e.Cluster.Targets {
		targets[key] = runtime
	}
	targets[name] = target
	for _, runtime := range targets {
		next := catalog.CloneState(runtime.Catalog.State)
		if runtime == target {
			e.Catalog.RetainDatabasesLocked(next)
			e.Catalog.InheritSubscriptionsLocked(next)
			e.Catalog.InheritParametersLocked(next)
			e.Catalog.InheritTablespacesLocked(next)
		}
		next.SetDescriptor(address, database)
		next.BindName(catalog.DescriptorNameKey(database), address)
		roots[&runtime.Catalog] = next
	}
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	target.Catalog.Database = address
	target.Cluster = e.Cluster
	roots.InstallLocked()
	target.Statistics.InheritBackendRegistrations(&e.Statistics)
	// Read authority here, after template preparation and final admission:
	// a shared reset may have occurred while the cluster mutex was released.
	target.Statistics.ResetShared(statistics.Archiver, e.Cluster.ArchiverResetAt)
	target.Statistics.ResetShared(statistics.BGWriter, e.Cluster.BGWriterResetAt)
	e.Cluster.Targets = targets
	e.Cluster.NextOID = oid + 1
	return core.Result{CommandTag: "CREATE DATABASE"}, nil
}

func databaseTemplatePermissionLocked(e *core.Engine, scope *core.StatementScope, database *catalog.Database) error {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return err
	}
	if database.IsTemplate {
		return nil
	}
	allowed, err := auth.Roles.InheritsRole(auth.State.CurrentUser, database.Owner)
	if err != nil {
		return err
	}
	if !allowed {
		return core.SQLError("42501", fmt.Sprintf("permission denied to copy database \"%s\"", database.Name()))
	}
	return nil
}

func databaseTemplateCurrentLocked(e *core.Engine, scope *core.StatementScope, source *core.Engine, observed *catalog.Database, epoch ids.CatalogEpoch) error {
	if e.Cluster.Targets[observed.Name()] != source || source.Cluster != e.Cluster || source.Catalog.State.Epoch() != epoch {
		return catalog.Conflict{}
	}
	current := source.Catalog.State.MustDatabase(source.Catalog.DatabaseAddress())
	if !catalog.DatabaseAuthorityCompatible(current, observed) {
		return catalog.Conflict{}
	}
	return databaseTemplatePermissionLocked(e, scope, current)
}
