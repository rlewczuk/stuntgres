package engine

import (
	"context"
	"reflect"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

const foreignHandlerSignature procedureSignature = ""
const foreignValidatorSignature procedureSignature = "1009,26"

type foreignProcedure struct {
	name      ForeignFunctionName
	wrapper   ForeignDataWrapper
	validator func(context.Context, ForeignObjectKind, []ForeignOption) error
}

func foreignFunctionName(name ForeignFunctionName) (ForeignFunctionName, error) {
	if name.Schema == "" {
		name.Schema = "public"
	}
	for _, part := range []string{name.Schema, name.Name} {
		if part == "" || len(part) > 63 || !utf8.ValidString(part) || strings.IndexByte(part, 0) >= 0 {
			return ForeignFunctionName{}, RegistrationError("invalid foreign procedure identifier")
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

// InstallForeignDefinitions runs only before an Engine becomes reachable from
// its Driver. It never invokes application code. Later catalog publication uses
// immutable procedure tokens, not a mutable name-to-callback fallback.
func (e *Engine) InstallForeignDefinitions(wrappers []ForeignDataWrapperDefinition, validators []ForeignValidatorDefinition, importer ForeignValueImporter, exporter ForeignValueExporter) error {
	e.foreignValueImporter = importer
	e.foreignValueExporter = exporter
	e.foreignWrappers = append([]ForeignDataWrapperDefinition(nil), wrappers...)
	e.foreignValidators = append([]ForeignValidatorDefinition(nil), validators...)
	owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	install := func(name ForeignFunctionName, p *foreignProcedure, result ids.OID, signature procedureSignature) error {
		namespace := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Schema})
		if namespace == (ids.ObjectAddress{}) {
			oid, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}
			ns := &namespaceDescriptor{address: namespace, name: name.Schema, revision: 1, owner: owner}
			e.catalog.state.setDescriptor(namespace, ns)
			e.catalog.state.bindName(descriptorNameKey(ns), namespace)
			if e.foreignNamespaces == nil {
				e.foreignNamespaces = make(map[ids.ObjectAddress]string)
			}
			e.foreignNamespaces[namespace] = name.Schema
		}
		key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: namespace, Name: name.Name, Signature: string(signature)}
		if e.catalog.state.NameAddress(key) != (ids.ObjectAddress{}) {
			return RegistrationError("foreign procedure signature already installed")
		}
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		d := &procedureDescriptor{address: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, namespace: namespace, revision: 1, name: name.Name, signature: signature, owner: owner, routineKind: functionRoutine, result: result, strict: true, volatility: expr.Volatile, foreign: p}
		e.catalog.state.setDescriptor(d.address, d)
		e.catalog.state.bindName(key, d.address)
		return nil
	}
	for _, def := range wrappers {
		if err := install(def.HandlerName, &foreignProcedure{name: def.HandlerName, wrapper: def.Wrapper}, 3115, foreignHandlerSignature); err != nil {
			return err
		}
	}
	for _, def := range validators {
		if err := install(def.Name, &foreignProcedure{name: def.Name, validator: def.Validate}, 2278, foreignValidatorSignature); err != nil {
			return err
		}
	}
	return nil
}
