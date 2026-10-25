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
	target := &Engine{Mu: e.Mu, RoleStore: e.RoleStore, Settings: e.Settings, Foreign: seed.Foreign, DatabaseName: name, DatabaseFrozenXID: 3}
	target.Catalog = catalog.NewStore(catalog.Owner{Principals: &e.RoleStore.Domain, ACLAuthority: &e.RoleStore.ACLAuthority, Providers: make(map[ids.OID]catalog.Provider, len(e.Catalog.Providers)), Extensions: e.Catalog.Extensions}, seed.Catalog.NextOID(), seed.Catalog.NextRevision())
	for class, provider := range e.Catalog.Providers {
		target.Catalog.Providers[class] = provider
	}
	target.Catalog.Providers[ids.ParameterClassID] = role.ParameterProvider{Settings: &target.Settings}
	target.Analysis = BindingHost{Engine: target}
	target.Authority = AuthorityInputs{Domain: target.Catalog.Domain, Settings: &target.Settings, Storage: &target.Storage}
	target.Visibility = visibility.NewRegistry(e.Visibility.TracksCommitTimestamps())
	target.Locks = e.Cluster.Locks
	target.Storage.ReplaceRelationsLocked(make(map[ids.ObjectAddress]*storage.Relation))
	target.Storage.ReplaceSequencesLocked(make(map[ids.ObjectAddress]*storage.SequenceGenerator))
	target.Catalog.PublishLocked(catalog.CloneState(seed.Catalog.State))
	previous := target.Catalog.State.MustDatabase(target.Catalog.DatabaseAddress())
	target.Catalog.State.UnbindName(catalog.DescriptorNameKey(previous))
	database := &catalog.Database{ObjectAddress: previous.Address(), ObjectRevision: 1, ObjectName: name, Owner: owner, FrozenXID: 3}
	target.Catalog.State.SetDescriptor(database.Address(), database)
	target.Catalog.State.BindName(catalog.DescriptorNameKey(database), database.Address())
	return target
}
