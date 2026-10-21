package fdw

import (
	"iter"
	"maps"
	"reflect"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

func FunctionName(name catalog.ForeignFunctionName) (catalog.ForeignFunctionName, error) {
	if name.Schema == "" {
		name.Schema = "public"
	}
	for _, part := range []string{name.Schema, name.Name} {
		if part == "" || len(part) > 63 || !utf8.ValidString(part) || strings.IndexByte(part, 0) >= 0 {
			return catalog.ForeignFunctionName{}, registrationError("invalid foreign procedure identifier")
		}
	}
	return name, nil
}

func ValidateWrapper(def WrapperDefinition) (WrapperDefinition, error) {
	name, err := FunctionName(def.HandlerName)
	if err != nil {
		return WrapperDefinition{}, err
	}
	if def.Wrapper == nil {
		return WrapperDefinition{}, registrationError("nil foreign-data wrapper")
	}
	value := reflect.ValueOf(def.Wrapper)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return WrapperDefinition{}, registrationError("nil foreign-data wrapper")
		}
	}
	def.HandlerName = name
	return def, nil
}

func ValidateValidator(def ValidatorDefinition) (ValidatorDefinition, error) {
	name, err := FunctionName(def.Name)
	if err != nil {
		return ValidatorDefinition{}, err
	}
	if def.Validate == nil {
		return ValidatorDefinition{}, registrationError("nil foreign validator")
	}
	def.Name = name
	return def, nil
}

// Registry holds the wrappers, validators and value codecs the driver
// installs, and the namespaces installation created. Engine.mu guards it. It
// is written only by InstallForeignDefinitions before the Engine is
// reachable; afterwards the namespace map is never mutated, only replaced
// whole by a Restore publication.
type Registry struct {
	wrappers    []WrapperDefinition
	validators  []ValidatorDefinition
	namespaces  map[ids.ObjectAddress]string // Immutable auto-installed namespace identities.
	importValue exec.ForeignValueImporter
	exportValue exec.ForeignValueExporter
}

// Install records private copies of the definitions and the value codecs.
func (r *Registry) Install(wrappers []WrapperDefinition, validators []ValidatorDefinition, importer exec.ForeignValueImporter, exporter exec.ForeignValueExporter) {
	r.importValue = importer
	r.exportValue = exporter
	r.wrappers = append([]WrapperDefinition(nil), wrappers...)
	r.validators = append([]ValidatorDefinition(nil), validators...)
}

// Definitions returns the installed definitions in InstallForeignDefinitions'
// argument order, for re-installing them into a fresh Engine.
func (r *Registry) Definitions() ([]WrapperDefinition, []ValidatorDefinition, exec.ForeignValueImporter, exec.ForeignValueExporter) {
	return r.wrappers, r.validators, r.importValue, r.exportValue
}

// AddNamespace records a namespace that installation created. It runs only
// during installation, before the map is published.
func (r *Registry) AddNamespace(address ids.ObjectAddress, name string) {
	if r.namespaces == nil {
		r.namespaces = make(map[ids.ObjectAddress]string)
	}
	r.namespaces[address] = name
}

// AdoptNamespaces replaces the namespace map with from's. The Restore
// publication calls it in the same step that replaces the catalog state.
func (r *Registry) AdoptNamespaces(from Registry) { r.namespaces = from.namespaces }

// NamespaceName reports the configured name of an installed namespace.
func (r *Registry) NamespaceName(address ids.ObjectAddress) (string, bool) {
	name, ok := r.namespaces[address]
	return name, ok
}

// NamespaceEntries yields every installed namespace and its configured name.
func (r *Registry) NamespaceEntries() iter.Seq2[ids.ObjectAddress, string] {
	return maps.All(r.namespaces)
}

// Codecs returns the driver value codecs; ok is false until both are installed.
func (r *Registry) Codecs() (exec.ForeignValueImporter, exec.ForeignValueExporter, bool) {
	return r.importValue, r.exportValue, r.importValue != nil && r.exportValue != nil
}
