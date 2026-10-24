package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// newDatabaseRuntime allocates only database-local execution state. Immutable
// host provider configuration is retained; ReplaceCatalog remints the real seed
// catalog and storage before this runtime can be registered.
func (e *Engine) newDatabaseRuntime(seed *Engine, name string, owner authorization.PrincipalRef) *Engine {
	target := &Engine{mu: e.mu, roles: e.roles, settings: e.settings, foreign: seed.foreign, databaseName: name, databaseFrozenXID: 3}
	target.catalog = catalog.NewStore(catalog.Owner{Principals: &e.roles.Domain, ACLAuthority: &e.roles.ACLAuthority, Providers: make(map[ids.OID]catalog.Provider, len(e.catalog.Providers)), Extensions: e.catalog.Extensions}, seed.catalog.NextOID(), seed.catalog.NextRevision())
	for class, provider := range e.catalog.Providers {
		target.catalog.Providers[class] = provider
	}
	target.catalog.Providers[ids.ParameterClassID] = role.ParameterProvider{Settings: &target.settings}
	target.analysis = bindingHost{engine: target}
	target.authority = authorityInputs{domain: target.catalog.Domain, settings: &target.settings, storage: &target.storage}
	target.visibility = visibility.NewRegistry(e.visibility.TracksCommitTimestamps())
	target.locks = e.cluster.locks
	target.storage.ReplaceRelationsLocked(make(map[ids.ObjectAddress]*storage.Relation))
	target.storage.ReplaceSequencesLocked(make(map[ids.ObjectAddress]*storage.SequenceGenerator))
	target.catalog.PublishLocked(catalog.CloneState(seed.catalog.State))
	previous := target.catalog.State.MustDatabase(target.catalog.DatabaseAddress())
	target.catalog.State.UnbindName(catalog.DescriptorNameKey(previous))
	database := &catalog.Database{ObjectAddress: previous.Address(), ObjectRevision: 1, ObjectName: name, Owner: owner, FrozenXID: 3}
	target.catalog.State.SetDescriptor(database.Address(), database)
	target.catalog.State.BindName(catalog.DescriptorNameKey(database), database.Address())
	return target
}
