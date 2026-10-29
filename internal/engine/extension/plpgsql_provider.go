package extension

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// PLpgSQLProvider registers the stock bootstrap script. Handler execution stays
// under catalog.PLHandler authority; this is not a scalar ABI or language DDL.
type PLpgSQLProvider struct{}

var StockPLpgSQL = &PLpgSQLProvider{}

func (*PLpgSQLProvider) Control() catalog.ExtensionControl {
	return catalog.ExtensionControl{Version: "1.0", Trusted: true, Superuser: true, Relocatable: false, Schema: new("pg_catalog"), Comment: new("PL/pgSQL procedural language")}
}
func (*PLpgSQLProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func (p *PLpgSQLProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}

func (p *PLpgSQLProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	if p != StockPLpgSQL || ext.Version != "1.0" || ext.TargetNamespace != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) {
		return fmt.Errorf("stuntgres: invalid plpgsql script target")
	}
	ext.Members = make([]ids.ObjectAddress, 4)
	languageOID, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	language := &catalog.Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: languageOID}, ObjectRevision: revision, ObjectName: "plpgsql", Extension: ext.Address(), Owner: ext.Owner, Procedural: true, Trusted: true}
	for i, kind := range []catalog.PLHandler{catalog.PLCallHandler, catalog.PLInlineHandler, catalog.PLValidator} {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		handler := kind.ReleasedProcedure()
		handler.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		handler.ObjectNamespace = ext.TargetNamespace
		handler.ObjectRevision = revision
		handler.Owner = owner
		handler.Extension = ext.Address()
		ext.Members[i] = handler.Address()
		switch kind {
		case catalog.PLCallHandler:
			language.Call = handler.Address()
		case catalog.PLInlineHandler:
			language.Inline = handler.Address()
		case catalog.PLValidator:
			language.Validator = handler.Address()
		}
		if err := stage.Create(&handler); err != nil {
			return err
		}
	}
	ext.Members[3] = language.Address()
	return stage.Create(language)
}

func (p *PLpgSQLProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if len(e.Members) != 4 || e.Version != "1.0" {
		return InvalidProviderDescriptor()
	}
	for i, a := range e.Members {
		if i == 3 {
			language, ok := v.Get(a).(*catalog.Language)
			if !ok || language.Name() != "plpgsql" {
				return InvalidProviderDescriptor()
			}
			if err := (catalog.LanguageProvider{}).Validate(v, language); err != nil {
				return err
			}
		} else {
			handler, ok := v.Procedure(a)
			if !ok || handler.PLHandler != catalog.PLHandler(i+1) {
				return InvalidProviderDescriptor()
			}
			if err := catalog.ValidatePLHandler(v, handler); err != nil {
				return err
			}
		}
	}
	return nil
}
