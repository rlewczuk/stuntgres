package engine

import (
	"fmt"
	"iter"
	"sync"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Every engine seeds the same built-in catalog from compiled tables; only the
// current database descriptor depends on engine options. Work whose result is
// a property of the seed is done once per process and shared by every engine.

// builtinCatalogCapacity is the number of descriptors and names a fresh engine
// seeds from the built-in tables, plus room for the few seeded outside them:
// namespaces, tablespaces, catalog sources and the current database.
func builtinCatalogCapacity(types int) int {
	return types + len(catalog.BuiltinCollations) + len(builtinConversionRoutines) + len(builtinRoutineDeclarations) +
		len(catalog.BuiltinConversions) + len(builtinOperators) + 128
}

// builtinProcedureNames is the procedure-name index of the seeded procedures.
// Engines share it read-only: ProcedureIndexEdit copies its maps and buckets
// before the first change, as for seededOperatorNames.
var builtinProcedureNames struct {
	once  sync.Once
	index *catalog.ProcedureNameIndex
}

// seedProcedureNames installs the shared index, building it from this
// engine's seed when it is the first in the process.
func (e *Engine) seedProcedureNames() {
	builtinProcedureNames.once.Do(func() {
		builtinProcedureNames.index = catalog.RebuildProcedureNames(e.catalog.state.Descriptors())
	})
	e.catalog.state.SetProcedureNames(builtinProcedureNames.index)
}

// builtinCatalogValidation memoizes provider validation of the built-in seed.
// The seed is fixed by the binary, so the first engine's result is every
// engine's result.
var builtinCatalogValidation struct {
	once sync.Once
	err  error
}

// validateSeed validates the built-in seed once per process and the current
// database descriptor, which depends on engine options, on every engine.
func (e *Engine) validateSeed() error {
	builtinCatalogValidation.once.Do(func() {
		builtinCatalogValidation.err = e.validateCatalogDescriptors(e.catalog.state.Descriptors())
	})
	if builtinCatalogValidation.err != nil {
		return builtinCatalogValidation.err
	}
	database := e.catalog.state.Get(catalog.CurrentDatabaseAddress)
	return e.validateCatalogDescriptors(func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		yield(catalog.CurrentDatabaseAddress, database)
	})
}

// validateCatalogDescriptors checks each built-in descriptor with the
// provider registered for its class.
func (e *Engine) validateCatalogDescriptors(descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) error {
	view := e.catalog.read(e.catalog.state)
	for _, d := range descriptors {
		p := e.catalog.Provider(d.Address().ClassID)
		if p == nil {
			return fmt.Errorf("stuntgres: missing catalog provider for built-in %q (class %d)", d.Name(), d.Address().ClassID)
		}
		if err := p.Validate(view, d); err != nil {
			return fmt.Errorf("stuntgres: invalid built-in catalog descriptor %q (class %d): %w", d.Name(), d.Address().ClassID, err)
		}
	}
	return nil
}
