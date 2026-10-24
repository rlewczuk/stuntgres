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

func (e *Engine) databaseCreateOwnerLocked(scope *StatementScope, name string) (authorization.PrincipalRef, error) {
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return authorization.PrincipalRef{}, err
	}
	return ddl.CreateDatabaseOwner(auth.Roles, auth.State.CurrentUser, name)
}

func (e *Engine) executeCreateDatabase(scope *StatementScope, statement *parser.CreateDatabaseStmt, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	ctx := scope.op.Context()
	options, err := ddl.ParseCreateDatabaseOptions(statement.Options)
	if err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	owner, err := e.databaseCreateOwnerLocked(scope, options.Owner)
	if err == nil {
		err = ddl.CheckCreateDatabase(statement.Name, options, func(name string) bool { return e.cluster.targets[name] != nil })
	}
	if err == nil {
		err = ctx.Err()
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err := scope.acquireRolePrincipal(owner, lock.AccessShare, false); err != nil {
		return Result{}, err
	}
	if err := scope.roleStillPresent(owner); err != nil {
		return Result{}, err
	}
	return e.createDatabaseTarget(ctx, scope, statement.Name, options.Owner, owner)
}

// createDatabaseTarget constructs storage privately. Only the final no-failure
// installation below can expose a name, descriptor, target or allocator advance.
func (e *Engine) createDatabaseTarget(ctx context.Context, scope *StatementScope, name, ownerName string, owner authorization.PrincipalRef) (Result, error) {
	e.cluster.seedOnce.Do(func() {
		candidate, err := newEngine(nil, nil, "template1", e.visibility.TracksCommitTimestamps())
		if err == nil {
			err = candidate.InstallForeignDefinitions(e.foreign.Definitions())
		}
		e.cluster.seed, e.cluster.seedError = candidate, err
	})
	if e.cluster.seedError != nil {
		return Result{}, e.cluster.seedError
	}
	seed := e.cluster.seed
	target := e.newDatabaseRuntime(seed, name, owner)
	if err := e.locks.AdmitDomain(target.catalog.Domain); err != nil {
		return Result{}, err
	}
	if _, err := target.replaceCatalog(ctx, seed, seed.CatalogRead(), nil); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	currentOwner, err := e.databaseCreateOwnerLocked(scope, ownerName)
	if err != nil {
		return Result{}, err
	}
	if currentOwner != owner {
		return Result{}, catalog.Conflict{}
	}
	scope.op.Session.Mu.Lock()
	defer scope.op.Session.Mu.Unlock()
	if err := publicationOperationLocked(e, scope.op); err != nil {
		return Result{}, err
	}
	if scope.txRef != (ids.TxRef{}) && scope.txRef.Epoch() != e.catalog.State.Epoch() {
		return Result{}, ids.ErrCatalogReplaced
	}
	result, err := e.publishDatabaseTargetLocked(ctx, name, target, owner)
	if err == nil {
		scope.completed = true
	}
	return result, err
}

func (e *Engine) publishDatabaseTargetLocked(ctx context.Context, name string, target *Engine, owner authorization.PrincipalRef) (Result, error) {
	if e.cluster.targets[name] != nil {
		return Result{}, sqlError("42P04", fmt.Sprintf(`database "%s" already exists`, name))
	}
	if target == nil || target.mu != e.mu || target.roles != e.roles || target.cluster != nil || target.databaseName != name || target.catalog.Domain == e.catalog.Domain || target.catalog.Principals != &e.roles.domain || target.catalog.ACLAuthority != &e.roles.aclAuthority {
		return Result{}, fmt.Errorf("stuntgres: foreign or already registered database target")
	}
	if _, err := e.roles.published.Root.Read(authorization.PrincipalRef{}).Descriptor(owner); err != nil {
		return Result{}, err
	}
	oid, err := e.databaseOIDLocked(target)
	if err != nil {
		return Result{}, err
	}
	address := ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: oid}
	database := &catalog.Database{ObjectAddress: address, ObjectRevision: 1, ObjectName: name, Owner: owner, FrozenXID: target.databaseFrozenXID}
	targets := make(map[string]*Engine, len(e.cluster.targets)+1)
	roots := make(map[*Engine]*catalog.State, len(e.cluster.targets)+1)
	for key, runtime := range e.cluster.targets {
		targets[key] = runtime
	}
	targets[name] = target
	for _, runtime := range targets {
		next := catalog.CloneState(runtime.catalog.State)
		if runtime == target {
			for a, d := range next.Descriptors() {
				if a.ClassID == ids.DatabaseClassID {
					next.UnbindName(catalog.DescriptorNameKey(d))
					next.RemoveDescriptor(a)
				}
			}
			for a, d := range e.catalog.State.Descriptors() {
				if a.ClassID == ids.DatabaseClassID {
					next.SetDescriptor(a, d)
					next.BindName(catalog.DescriptorNameKey(d), a)
				}
			}
		}
		next.SetDescriptor(address, database)
		next.BindName(catalog.DescriptorNameKey(database), address)
		roots[runtime] = next
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	target.catalog.Database = address
	target.cluster = e.cluster
	installDatabaseProjectionLocked(roots)
	e.cluster.targets = targets
	e.cluster.nextOID = oid + 1
	return Result{CommandTag: "CREATE DATABASE"}, nil
}
