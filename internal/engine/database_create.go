package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func databaseCreateOwnerLocked(e *Engine, scope *StatementScope, name string) (authorization.PrincipalRef, error) {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return authorization.PrincipalRef{}, err
	}
	return ddl.CreateDatabaseOwner(auth.Roles, auth.State.CurrentUser, name)
}

func executeCreateDatabase(e *Engine, scope *StatementScope, statement *parser.CreateDatabaseStmt, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	ctx := scope.Op.Context()
	options, err := ddl.ParseCreateDatabaseOptions(statement.Options)
	if err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	owner, err := databaseCreateOwnerLocked(e, scope, options.Owner)
	if err == nil {
		err = ddl.CheckCreateDatabase(statement.Name, options, func(name string) bool { return e.Cluster.Targets[name] != nil })
	}
	if err == nil {
		err = ctx.Err()
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err := scope.AcquireRolePrincipal(owner, lock.AccessShare, false); err != nil {
		return Result{}, err
	}
	if err := scope.RoleStillPresent(owner); err != nil {
		return Result{}, err
	}
	return createDatabaseTarget(e, ctx, scope, statement.Name, options.Owner, owner)
}

// createDatabaseTarget constructs storage privately. Only the final no-failure
// installation below can expose a name, descriptor, target or allocator advance.
func createDatabaseTarget(e *Engine, ctx context.Context, scope *StatementScope, name, ownerName string, owner authorization.PrincipalRef) (Result, error) {
	e.Cluster.SeedOnce.Do(func() {
		candidate, err := e.Factory(nil, "template1", e.Visibility.TracksCommitTimestamps())
		if err == nil {
			err = candidate.InstallForeignDefinitions(e.Foreign.Definitions())
		}
		e.Cluster.Seed, e.Cluster.SeedError = candidate, err
	})
	if e.Cluster.SeedError != nil {
		return Result{}, e.Cluster.SeedError
	}
	seed := e.Cluster.Seed
	target := newDatabaseRuntime(e, seed, name, owner)
	if err := e.Locks.AdmitDomain(target.Catalog.Domain); err != nil {
		return Result{}, err
	}
	if _, err := target.ReplaceCatalogInOperation(ctx, seed, seed.CatalogRead(), nil); err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	currentOwner, err := databaseCreateOwnerLocked(e, scope, ownerName)
	if err != nil {
		return Result{}, err
	}
	if currentOwner != owner {
		return Result{}, catalog.Conflict{}
	}
	scope.Op.Session.Mu.Lock()
	defer scope.Op.Session.Mu.Unlock()
	if err := PublicationOperationLocked(e, scope.Op); err != nil {
		return Result{}, err
	}
	if scope.TxRef != (ids.TxRef{}) && scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return Result{}, ids.ErrCatalogReplaced
	}
	result, err := publishDatabaseTargetLocked(e, ctx, name, target, owner)
	if err == nil {
		scope.Completed = true
	}
	return result, err
}

func publishDatabaseTargetLocked(e *Engine, ctx context.Context, name string, target *Engine, owner authorization.PrincipalRef) (Result, error) {
	if e.Cluster.Targets[name] != nil {
		return Result{}, SQLError("42P04", fmt.Sprintf(`database "%s" already exists`, name))
	}
	if target == nil || target.Mu != e.Mu || target.RoleStore != e.RoleStore || target.Cluster != nil || target.DatabaseName != name || target.Catalog.Domain == e.Catalog.Domain || target.Catalog.Principals != &e.RoleStore.Domain || target.Catalog.ACLAuthority != &e.RoleStore.ACLAuthority {
		return Result{}, fmt.Errorf("stuntgres: foreign or already registered database target")
	}
	if _, err := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Descriptor(owner); err != nil {
		return Result{}, err
	}
	oid, err := catalog.DatabaseOIDLocked(e.Cluster.NextOID, &target.Catalog, e.Cluster.Catalogs)
	if err != nil {
		return Result{}, err
	}
	address := ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: oid}
	database := &catalog.Database{ObjectAddress: address, ObjectRevision: 1, ObjectName: name, Owner: owner, FrozenXID: target.DatabaseFrozenXID}
	targets := make(map[string]*Engine, len(e.Cluster.Targets)+1)
	roots := make(catalog.DatabaseProjection, len(e.Cluster.Targets)+1)
	for key, runtime := range e.Cluster.Targets {
		targets[key] = runtime
	}
	targets[name] = target
	for _, runtime := range targets {
		next := catalog.CloneState(runtime.Catalog.State)
		if runtime == target {
			e.Catalog.RetainDatabasesLocked(next)
		}
		next.SetDescriptor(address, database)
		next.BindName(catalog.DescriptorNameKey(database), address)
		roots[&runtime.Catalog] = next
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	target.Catalog.Database = address
	target.Cluster = e.Cluster
	roots.InstallLocked()
	e.Cluster.Targets = targets
	e.Cluster.NextOID = oid + 1
	return Result{CommandTag: "CREATE DATABASE"}, nil
}
