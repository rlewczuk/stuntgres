package stuntgres

import "stuntgres/internal/engine"

// Foreign callback types use only standard Go values. Registration installs
// executable capabilities, not PostgreSQL C functions or remote transports.
type ForeignOption = engine.ForeignOption
type ForeignObjectKind = engine.ForeignObjectKind
type ForeignFunctionName = engine.ForeignFunctionName
type ForeignDataWrapperDefinition = engine.ForeignDataWrapperDefinition
type ForeignValidatorDefinition = engine.ForeignValidatorDefinition
type ForeignColumn = engine.ForeignColumn
type ForeignScanParameters = engine.ForeignScanParameters
type ForeignRequest = engine.ForeignRequest
type ForeignRelSize = engine.ForeignRelSize
type ForeignPath = engine.ForeignPath
type ForeignPlan = engine.ForeignPlan
type ForeignRow = engine.ForeignRow
type ForeignDataWrapper = engine.ForeignDataWrapper
type ForeignScan = engine.ForeignScan
type ForeignImportRequest = engine.ForeignImportRequest
type ForeignColumnDefinition = engine.ForeignColumnDefinition
type ForeignTableDefinition = engine.ForeignTableDefinition
type ForeignSchemaImporter = engine.ForeignSchemaImporter

const (
	ForeignAttribute   = engine.ForeignAttribute
	ForeignWrapper     = engine.ForeignWrapper
	ForeignServer      = engine.ForeignServer
	ForeignUserMapping = engine.ForeignUserMapping
	ForeignTable       = engine.ForeignTable
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
