package stuntgres

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/remote"
)

type ExternalSQLProviderDefinition = catalog.ExternalSQLProviderDefinition
type ExternalSQLProvider = catalog.ExternalSQLProvider
type ExternalSQLSession = catalog.ExternalSQLSession
type ExternalSQLConnectRequest = catalog.ExternalSQLConnectRequest
type ExternalSQLConnection = catalog.ExternalSQLConnection
type ExternalSQLConnectionInfo = catalog.ExternalSQLConnectionInfo
type ExternalSQLColumn = catalog.ExternalSQLColumn
type ExternalSQLCell = catalog.ExternalSQLCell
type ExternalSQLResult = catalog.ExternalSQLResult
type ExternalSQLNotification = catalog.ExternalSQLNotification
type ExternalSQLUnavailableError = catalog.ExternalSQLUnavailableError
type ExternalSQLConnectError = catalog.ExternalSQLConnectError

// RegisterExternalSQLProvider installs a host-owned transport capability, never
// opens it, and freezes at the first successful connector construction. Bind a
// registered foreign handler through its definition's ExternalProvider name.
// Driver DSNs remain opaque; transport/authentication/TLS belong to the host.
func (d *Driver) RegisterExternalSQLProvider(def ExternalSQLProviderDefinition) error {
	if err := remote.ValidateDefinition(def); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settingsFrozen {
		return core.RegistrationError("external SQL registry is frozen")
	}
	for _, old := range d.externalProviders {
		if old.Name == def.Name {
			return core.RegistrationError("duplicate external SQL provider")
		}
	}
	d.externalProviders = append(d.externalProviders, def)
	return nil
}

// BindDblinkExternalSQLProvider selects the registered host transport for direct
// dblink conninfo. SQL names and opaque Driver DSNs cannot select a provider.
func (d *Driver) BindDblinkExternalSQLProvider(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settingsFrozen {
		return core.RegistrationError("external SQL registry is frozen")
	}
	if d.dblinkProvider != "" {
		return core.RegistrationError("dblink external SQL provider is already bound")
	}
	for _, provider := range d.externalProviders {
		if name != "" && provider.Name == name {
			d.dblinkProvider = name
			return nil
		}
	}
	return core.RegistrationError("unregistered dblink external SQL provider")
}

// BindPostgresFDWExternalSQLProvider selects the genuine host transport used by
// installed postgres_fdw. Registration and binding perform no external I/O.
func (d *Driver) BindPostgresFDWExternalSQLProvider(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settingsFrozen {
		return core.RegistrationError("external SQL registry is frozen")
	}
	if d.postgresFDWProvider != "" {
		return core.RegistrationError("postgres_fdw external SQL provider is already bound")
	}
	for _, provider := range d.externalProviders {
		if name != "" && provider.Name == name {
			d.postgresFDWProvider = name
			return nil
		}
	}
	return core.RegistrationError("unregistered postgres_fdw external SQL provider")
}
