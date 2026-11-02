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

// RegisterExternalSQLProvider installs a host-owned transport capability, never
// opens it, and freezes at the first successful connector construction. Bind a
// registered foreign handler through its definition's ExternalProvider name.
// The engine does not parse Driver DSNs, libpq conninfo, credentials or TLS.
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
