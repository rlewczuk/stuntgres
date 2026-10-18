package stuntgres

import (
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
)

// Foreign callback types use only standard Go values. Registration installs
// executable capabilities, not PostgreSQL C functions or remote transports.
type ForeignOption = catalog.ForeignOption
type ForeignObjectKind = catalog.ForeignObjectKind
type ForeignFunctionName = catalog.ForeignFunctionName
type ForeignDataWrapperDefinition = engine.ForeignDataWrapperDefinition
type ForeignValidatorDefinition = engine.ForeignValidatorDefinition
type ForeignColumn = catalog.ForeignColumn
type ForeignScanParameters = catalog.ForeignScanParameters
type ForeignRequest = catalog.ForeignRequest
type ForeignRelSize = catalog.ForeignRelSize
type ForeignPath = catalog.ForeignPath
type ForeignPlan = catalog.ForeignPlan
type ForeignRow = catalog.ForeignRow
type ForeignDataWrapper = catalog.ForeignDataWrapper
type ForeignScan = catalog.ForeignScan
type ForeignImportRequest = engine.ForeignImportRequest
type ForeignColumnDefinition = engine.ForeignColumnDefinition
type ForeignTableDefinition = engine.ForeignTableDefinition
type ForeignSchemaImporter = engine.ForeignSchemaImporter

const (
	ForeignAttribute   = catalog.ForeignAttribute
	ForeignWrapper     = catalog.ForeignWrapper
	ForeignServer      = catalog.ForeignServer
	ForeignUserMapping = catalog.ForeignUserMapping
	ForeignTable       = catalog.ForeignTable
)

// RegisterForeignDataWrapper binds an actual implementation to a decoded SQL
// procedure name. Registrations freeze on first connector construction, are
// Driver-local, and apply to every exact-DSN database owned by that Driver.
func (d *Driver) RegisterForeignDataWrapper(def ForeignDataWrapperDefinition) error {
	v, err := engine.ValidateForeignDataWrapper(def)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settingsFrozen {
		return engine.RegistrationError("foreign registry is frozen")
	}
	for _, old := range d.foreignWrappers {
		if old.HandlerName == v.HandlerName {
			return engine.RegistrationError("duplicate foreign handler")
		}
	}
	d.foreignWrappers = append(d.foreignWrappers, v)
	return nil
}

// RegisterForeignValidator installs an option validator with the PostgreSQL
// (text[],oid) identity. Application callbacks never run during registration.
func (d *Driver) RegisterForeignValidator(def ForeignValidatorDefinition) error {
	v, err := engine.ValidateForeignValidator(def)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settingsFrozen {
		return engine.RegistrationError("foreign registry is frozen")
	}
	for _, old := range d.foreignValidators {
		if old.Name == v.Name {
			return engine.RegistrationError("duplicate foreign validator")
		}
	}
	d.foreignValidators = append(d.foreignValidators, v)
	return nil
}
