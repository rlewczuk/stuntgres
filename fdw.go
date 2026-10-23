package stuntgres

import (
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
)

// Foreign callback types use only standard Go values. Registration installs
// executable capabilities, not PostgreSQL C functions or remote transports.
type ForeignOption = catalog.ForeignOption
type ForeignObjectKind = catalog.ForeignObjectKind
type ForeignFunctionName = catalog.ForeignFunctionName
type ForeignDataWrapperDefinition = fdw.WrapperDefinition
type ForeignValidatorDefinition = fdw.ValidatorDefinition
type ForeignColumn = catalog.ForeignColumn
type ForeignScanParameters = catalog.ForeignScanParameters
type ForeignRequest = catalog.ForeignRequest
type ForeignRelSize = catalog.ForeignRelSize
type ForeignPath = catalog.ForeignPath
type ForeignPlan = catalog.ForeignPlan
type ForeignRow = catalog.ForeignRow
type ForeignDataWrapper = catalog.ForeignDataWrapper
type ForeignScan = catalog.ForeignScan
type ForeignParallelScanProvider = catalog.ForeignParallelScanProvider
type ForeignParallelScan = catalog.ForeignParallelScan
type ForeignImportRequest = fdw.ImportRequest
type ForeignColumnDefinition = fdw.ColumnDefinition
type ForeignTableDefinition = fdw.TableDefinition
type ForeignSchemaImporter = fdw.SchemaImporter
type ForeignModifyOperation = catalog.ForeignModifyOperation
type ForeignModifyPlan = catalog.ForeignModifyPlan
type ForeignUpdateTargets = catalog.ForeignUpdateTargets
type ForeignRowIdentity = catalog.ForeignRowIdentity
type ForeignModifyRequest = catalog.ForeignModifyRequest
type ForeignInsertRequest = catalog.ForeignInsertRequest
type ForeignTruncateRequest = catalog.ForeignTruncateRequest
type ForeignUpdateTargetProvider = catalog.ForeignUpdateTargetProvider
type ForeignModifyPlanner = catalog.ForeignModifyPlanner
type ForeignModifyBeginner = catalog.ForeignModifyBeginner
type ForeignModifyEnder = catalog.ForeignModifyEnder
type ForeignInserter = catalog.ForeignInserter
type ForeignUpdater = catalog.ForeignUpdater
type ForeignDeleter = catalog.ForeignDeleter
type ForeignBatchSizer = catalog.ForeignBatchSizer
type ForeignBatchInserter = catalog.ForeignBatchInserter
type ForeignTruncater = catalog.ForeignTruncater
type ForeignInsertBeginner = catalog.ForeignInsertBeginner
type ForeignInsertEnder = catalog.ForeignInsertEnder
type ForeignIdentityScan = catalog.ForeignIdentityScan
type ForeignTransactionCapabilities = catalog.ForeignTransactionCapabilities
type ForeignTransactionProvider = catalog.ForeignTransactionProvider
type ForeignTransaction = catalog.ForeignTransaction
type ForeignTransactionSavepoints = catalog.ForeignTransactionSavepoints

const (
	ForeignInsert = catalog.ForeignInsert
	ForeignUpdate = catalog.ForeignUpdate
	ForeignDelete = catalog.ForeignDelete
)

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
	v, err := fdw.ValidateWrapper(def)
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
	v, err := fdw.ValidateValidator(def)
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
