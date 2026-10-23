package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// newDatabaseRuntime allocates only database-local execution state. Immutable
// host provider configuration is retained; ReplaceCatalog remints the real seed
// catalog and storage before this runtime can be registered.
func (e *Engine) newDatabaseRuntime(seed *Engine, name string, owner authorization.PrincipalRef) *Engine {
	target := &Engine{mu: e.mu, roles: e.roles, settings: e.settings, foreign: seed.foreign, databaseName: name, databaseFrozenXID: 3}
	target.catalog = catalogStore{Owner: catalog.Owner{Domain: &ids.Domain{}, Principals: &e.roles.domain, ACLAuthority: &e.roles.aclAuthority, Providers: make(map[ids.OID]catalog.Provider, len(e.catalog.Providers)), Extensions: e.catalog.Extensions}, nextOID: seed.catalog.nextOID, nextRevision: seed.catalog.nextRevision}
	for class, provider := range e.catalog.Providers {
		target.catalog.Providers[class] = provider
	}
	target.catalog.Providers[ids.ParameterClassID] = role.ParameterProvider{Settings: &target.settings}
	target.analysis = bindingHost{engine: target}
	target.authority = authorityInputs{domain: target.catalog.Domain, settings: &target.settings, storage: &target.storage}
	target.visibility = visibility.NewRegistry(e.visibility.TracksCommitTimestamps())
	target.locks = lock.New(target.catalog.Domain)
	target.storage.ReplaceRelationsLocked(make(map[ids.ObjectAddress]*storage.Relation))
	target.storage.ReplaceSequencesLocked(make(map[ids.ObjectAddress]*storage.SequenceGenerator))
	target.catalog.state = catalog.CloneState(seed.catalog.state)
	previous := target.catalog.state.MustDatabase(target.catalog.DatabaseAddress())
	target.catalog.state.UnbindName(catalog.DescriptorNameKey(previous))
	database := &catalog.Database{ObjectAddress: previous.Address(), ObjectRevision: 1, ObjectName: name, Owner: owner, FrozenXID: 3}
	target.catalog.state.SetDescriptor(database.Address(), database)
	target.catalog.state.BindName(catalog.DescriptorNameKey(database), database.Address())
	return target
}
