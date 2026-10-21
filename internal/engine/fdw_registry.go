package engine

import (
	"errors"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
)

// RegistrationError reports an invalid adapter registration (foreign-data
// wrappers, settings, default providers) with the engine-wide prefix.
func RegistrationError(message string) error { return errors.New("stuntgres: " + message) }

// InstallForeignDefinitions runs only before an Engine becomes reachable from
// its Driver. It never invokes application code. Later catalog publication uses
// immutable procedure tokens, not a mutable name-to-callback fallback.
func (e *Engine) InstallForeignDefinitions(wrappers []fdw.WrapperDefinition, validators []fdw.ValidatorDefinition, importer exec.ForeignValueImporter, exporter exec.ForeignValueExporter) error {
	e.foreign.Install(wrappers, validators, importer, exporter)
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
			e.foreign.AddNamespace(namespace, name.Schema)
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
