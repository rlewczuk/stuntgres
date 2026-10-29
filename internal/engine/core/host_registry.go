package core

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
)

// InstallHostDefinitions is pure prepublication descriptor work. Each engine
// receives its own immutable tokens, even when definitions came from one Driver.
func (e *Engine) InstallHostDefinitions(definitions []*host.Runtime) error {
	if len(e.Catalog.HostRuntimes) != 0 {
		return fmt.Errorf("stuntgres: host inventory already installed")
	}
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	index := catalog.ProcedureIndexEdit{Index: e.Catalog.State.ProcedureNames()}
	for _, source := range definitions {
		r, err := host.New(source.Definition())
		if err != nil {
			return err
		}
		name := r.HandlerName()
		namespace := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Schema})
		if namespace == (ids.ObjectAddress{}) {
			oid, err := e.Catalog.AllocateOIDLocked()
			if err != nil {
				return err
			}
			namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}
			ns := &catalog.Namespace{ObjectAddress: namespace, ObjectName: name.Schema, ObjectRevision: 1, Owner: owner}
			e.Catalog.State.SetDescriptor(namespace, ns)
			e.Catalog.State.BindName(catalog.DescriptorNameKey(ns), namespace)
		}
		key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: namespace, Name: name.Name}
		langKey := ids.NameKey{ClassID: ids.LanguageClassID, Name: r.Name()}
		if e.Catalog.State.NameAddress(key) != (ids.ObjectAddress{}) || e.Catalog.State.NameAddress(langKey) != (ids.ObjectAddress{}) {
			return fmt.Errorf("stuntgres: host bootstrap name collision")
		}
		oid, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			return err
		}
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: namespace, ObjectRevision: 1, ObjectName: name.Name, Owner: owner, RoutineKind: catalog.FunctionRoutine, Result: 2280, Volatility: expr.Volatile, Parallel: 'u', Cost: 100, HostHandler: r, Display: catalog.RoutineDisplay{Language: catalog.GoRoutineLanguage, Source: r.Provider()}}
		e.Catalog.State.SetDescriptor(p.Address(), p)
		e.Catalog.State.BindName(key, p.Address())
		index.Replace(nil, p)
		oid, err = e.Catalog.AllocateOIDLocked()
		if err != nil {
			return err
		}
		language := &catalog.Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: oid}, ObjectRevision: 1, ObjectName: r.Name(), Owner: owner, Procedural: true, Trusted: r.Trusted(), Call: p.Address(), Host: r, HostRegistration: r}
		e.Catalog.State.SetDescriptor(language.Address(), language)
		e.Catalog.State.BindName(langKey, language.Address())
		e.Catalog.HostRuntimes = append(e.Catalog.HostRuntimes, r)
	}
	e.Catalog.State.SetProcedureNames(index.Index)
	if len(e.Catalog.HostRuntimes) != 0 {
		stage := e.BeginCatalogStage(e.Catalog.Current())
		defer e.DiscardCatalogStage(stage)
		if err := e.materializeHostConversions(stage, true, false, nil); err != nil {
			return err
		}
		edit := catalog.ProcedureIndexEdit{Index: e.Catalog.State.ProcedureNames()}
		for address, descriptor := range stage.Descriptors() {
			e.Catalog.State.SetDescriptor(address, descriptor)
			e.Catalog.State.BindName(catalog.DescriptorNameKey(descriptor), address)
			if p, ok := descriptor.(*catalog.Procedure); ok {
				edit.Replace(nil, p)
			}
		}
		e.Catalog.State.SetProcedureNames(edit.Index)
	}
	return nil
}
