package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

func (e *Engine) installBuiltinLanguages() error {
	owner := e.roles.domain.Principal(authorization.BootstrapPrincipalID)
	install := func(d catalog.Descriptor) {
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	for _, seed := range []struct {
		oid       ids.OID
		name      string
		validator ids.OID
		trusted   bool
	}{{12, "internal", 2246, false}, {13, "c", 2247, false}, {14, "sql", 2248, true}} {
		install(&catalog.Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: seed.oid}, ObjectRevision: 1, ObjectName: seed.name, Owner: owner, Trusted: seed.trusted, Validator: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: seed.validator}})
	}
	languageOID, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	language := &catalog.Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: languageOID}, ObjectRevision: 1, ObjectName: "plpgsql", Owner: owner, Procedural: true, Trusted: true}
	for _, kind := range []catalog.PLHandler{catalog.PLCallHandler, catalog.PLInlineHandler, catalog.PLValidator} {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		name, signature, result := kind.Declaration()
		handler := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectRevision: 1, ObjectName: name, Signature: signature, Result: result, Owner: owner, RoutineKind: catalog.FunctionRoutine, Volatility: expr.Volatile, PLHandler: kind}
		install(handler)
		switch kind {
		case catalog.PLCallHandler:
			language.Call = handler.Address()
		case catalog.PLInlineHandler:
			language.Inline = handler.Address()
		case catalog.PLValidator:
			language.Validator = handler.Address()
		}
	}
	install(language)
	return nil
}
