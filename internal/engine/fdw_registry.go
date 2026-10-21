package engine

import (
	"errors"
	"iter"
	"maps"
	"reflect"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// RegistrationError reports an invalid adapter registration (foreign-data
// wrappers, settings, default providers) with the engine-wide prefix.
func RegistrationError(message string) error { return errors.New("stuntgres: " + message) }

func foreignFunctionName(name catalog.ForeignFunctionName) (catalog.ForeignFunctionName, error) {
	if name.Schema == "" {
		name.Schema = "public"
	}
	for _, part := range []string{name.Schema, name.Name} {
		if part == "" || len(part) > 63 || !utf8.ValidString(part) || strings.IndexByte(part, 0) >= 0 {
			return catalog.ForeignFunctionName{}, RegistrationError("invalid foreign procedure identifier")
		}
	}
	return name, nil
}

func ValidateForeignDataWrapper(def ForeignDataWrapperDefinition) (ForeignDataWrapperDefinition, error) {
	name, err := foreignFunctionName(def.HandlerName)
	if err != nil {
		return ForeignDataWrapperDefinition{}, err
	}
	if def.Wrapper == nil {
		return ForeignDataWrapperDefinition{}, RegistrationError("nil foreign-data wrapper")
	}
	value := reflect.ValueOf(def.Wrapper)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return ForeignDataWrapperDefinition{}, RegistrationError("nil foreign-data wrapper")
		}
	}
	def.HandlerName = name
	return def, nil
}
func ValidateForeignValidator(def ForeignValidatorDefinition) (ForeignValidatorDefinition, error) {
	name, err := foreignFunctionName(def.Name)
	if err != nil {
		return ForeignValidatorDefinition{}, err
	}
	if def.Validate == nil {
		return ForeignValidatorDefinition{}, RegistrationError("nil foreign validator")
	}
	def.Name = name
	return def, nil
}

// foreignRegistry holds the wrappers, validators and value codecs the driver
// installs, and the namespaces installation created. Engine.mu guards it. It
// is written only by InstallForeignDefinitions before the Engine is
// reachable; afterwards the namespace map is never mutated, only replaced
// whole by a Restore publication.
type foreignRegistry struct {
	wrappers    []ForeignDataWrapperDefinition
	validators  []ForeignValidatorDefinition
	namespaces  map[ids.ObjectAddress]string // Immutable auto-installed namespace identities.
	importValue exec.ForeignValueImporter
	exportValue exec.ForeignValueExporter
}

// install records private copies of the definitions and the value codecs.
func (r *foreignRegistry) install(wrappers []ForeignDataWrapperDefinition, validators []ForeignValidatorDefinition, importer exec.ForeignValueImporter, exporter exec.ForeignValueExporter) {
	r.importValue = importer
	r.exportValue = exporter
	r.wrappers = append([]ForeignDataWrapperDefinition(nil), wrappers...)
	r.validators = append([]ForeignValidatorDefinition(nil), validators...)
}

// definitions returns the installed definitions in InstallForeignDefinitions'
// argument order, for re-installing them into a fresh Engine.
func (r *foreignRegistry) definitions() ([]ForeignDataWrapperDefinition, []ForeignValidatorDefinition, exec.ForeignValueImporter, exec.ForeignValueExporter) {
	return r.wrappers, r.validators, r.importValue, r.exportValue
}

// addNamespace records a namespace that installation created. It runs only
// during installation, before the map is published.
func (r *foreignRegistry) addNamespace(address ids.ObjectAddress, name string) {
	if r.namespaces == nil {
		r.namespaces = make(map[ids.ObjectAddress]string)
	}
	r.namespaces[address] = name
}

// adoptNamespaces replaces the namespace map with from's. The Restore
// publication calls it in the same step that replaces the catalog state.
func (r *foreignRegistry) adoptNamespaces(from foreignRegistry) { r.namespaces = from.namespaces }

// namespaceName reports the configured name of an installed namespace.
func (r *foreignRegistry) namespaceName(address ids.ObjectAddress) (string, bool) {
	name, ok := r.namespaces[address]
	return name, ok
}

// namespaceEntries yields every installed namespace and its configured name.
func (r *foreignRegistry) namespaceEntries() iter.Seq2[ids.ObjectAddress, string] {
	return maps.All(r.namespaces)
}

// codecs returns the driver value codecs; ok is false until both are installed.
func (r *foreignRegistry) codecs() (exec.ForeignValueImporter, exec.ForeignValueExporter, bool) {
	return r.importValue, r.exportValue, r.importValue != nil && r.exportValue != nil
}

// InstallForeignDefinitions runs only before an Engine becomes reachable from
// its Driver. It never invokes application code. Later catalog publication uses
// immutable procedure tokens, not a mutable name-to-callback fallback.
func (e *Engine) InstallForeignDefinitions(wrappers []ForeignDataWrapperDefinition, validators []ForeignValidatorDefinition, importer exec.ForeignValueImporter, exporter exec.ForeignValueExporter) error {
	e.foreign.install(wrappers, validators, importer, exporter)
	owner := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	install := func(name catalog.ForeignFunctionName, p *catalog.ForeignProcedure, result ids.OID, signature catalog.ProcedureSignature) error {
		namespace := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Schema})
		if namespace == (ids.ObjectAddress{}) {
			oid, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}
			ns := &catalog.Namespace{ObjectAddress: namespace, ObjectName: name.Schema, ObjectRevision: 1, Owner: owner}
			e.catalog.state.SetDescriptor(namespace, ns)
			e.catalog.state.BindName(catalog.DescriptorNameKey(ns), namespace)
			e.foreign.addNamespace(namespace, name.Schema)
		}
		key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: namespace, Name: name.Name, Signature: string(signature)}
		if e.catalog.state.NameAddress(key) != (ids.ObjectAddress{}) {
			return RegistrationError("foreign procedure signature already installed")
		}
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: namespace, ObjectRevision: 1, ObjectName: name.Name, Signature: signature, Owner: owner, RoutineKind: catalog.FunctionRoutine, Result: result, Strict: true, Volatility: expr.Volatile, Foreign: p}
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(key, d.Address())
		return nil
	}
	for _, def := range wrappers {
		if err := install(def.HandlerName, &catalog.ForeignProcedure{Name: def.HandlerName, Wrapper: def.Wrapper}, 3115, catalog.ForeignHandlerSignature); err != nil {
			return err
		}
	}
	for _, def := range validators {
		if err := install(def.Name, &catalog.ForeignProcedure{Name: def.Name, Validator: def.Validate}, 2278, catalog.ForeignValidatorSignature); err != nil {
			return err
		}
	}
	return nil
}
