package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type PostgresFDWProvider struct{ control catalog.ExtensionControl }

var StockPostgresFDW = &PostgresFDWProvider{control: catalog.ExtensionControl{Version: "1.2", Superuser: true, Relocatable: true, Comment: new("foreign-data wrapper for remote PostgreSQL servers")}}
var postgresFDWHandler = &catalog.ForeignProcedure{Wrapper: StockPostgresFDW}
var postgresFDWValidator = &catalog.ForeignProcedure{}

func (p *PostgresFDWProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*PostgresFDWProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.2"}}
}
func (p *PostgresFDWProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *PostgresFDWProvider) ValidateProcedure(v catalog.Read, d *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, d, p)
}
func (p *PostgresFDWProvider) SupportsValidator(d *catalog.Procedure) bool {
	return p == StockPostgresFDW && d != nil && d.Implementation == p && d.Foreign == postgresFDWValidator
}
func (p *PostgresFDWProvider) SupportsHandler(d *catalog.Procedure) bool {
	return p == StockPostgresFDW && d != nil && d.Implementation == p && d.Foreign == postgresFDWHandler
}
func (*PostgresFDWProvider) HandlerToken(token *catalog.ForeignProcedure) bool {
	return token == postgresFDWHandler
}
func (p *PostgresFDWProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockPostgresFDW || !currentManifest(v, e) || e.Name() != "postgres_fdw" || e.Version != "1.2" || len(e.Members) != 3 {
		return InvalidProviderDescriptor()
	}
	return validatePostgresFDWRecipe(v, e.Installation)
}

func postgresFDWDeclaration(slot int) (string, catalog.ProcedureSignature, ids.OID, *catalog.ForeignProcedure) {
	if slot == 0 {
		return "postgres_fdw_handler", catalog.ForeignHandlerSignature, 3115, postgresFDWHandler
	}
	return "postgres_fdw_validator", catalog.ForeignValidatorSignature, 2278, postgresFDWValidator
}
func postgresFDWProcedureShape(d *catalog.Procedure, slot int) bool {
	if d == nil || slot < 0 || slot > 1 {
		return false
	}
	name, signature, result, token := postgresFDWDeclaration(slot)
	return d.Implementation == StockPostgresFDW && d.Foreign == token && d.Signature == signature && d.Result == result && d.RoutineKind == catalog.FunctionRoutine && d.Strict && d.Volatility == expr.Volatile &&
		d.Installed == (catalog.InstalledRoutineDeclaration{Parallel: 'u'}) && d.Display == (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/postgres_fdw", HasBinary: true}) &&
		d.SQL == nil && d.Builtin == nil && d.Aggregate == nil && d.SQLBody == nil && d.SQLDefaults == nil && d.DeclarationBody == nil && d.LiteralDefaults == nil && d.InputNames == nil && d.RangeConstructor == nil && d.Host == nil && d.HostHandler == nil && d.HostConversion == nil && d.Sepgsql == nil && d.PLHandler == catalog.NoPLHandler && d.AccessMethod == catalog.NoAccessMethod && d.BrinSupport == 0 && d.GinSupport == 0 && d.Conversion.Name() == "" && d.Dictionary == (ids.ObjectAddress{}) && d.Language == (ids.ObjectAddress{}) && d.SnowballHelper == catalog.NoSnowballHelper && !d.SecurityDefiner && d.Configuration == nil && len(d.Body) == 0 && len(d.DeclarationDependencies) == 0 && d.Transforms == nil
}
func validatePostgresFDWRecipe(v catalog.Read, installation *catalog.Installation) error {
	if !installation.Valid() || installation.Provider != "postgres_fdw" || installation.Version != "1.2" || installation.Implementation != StockPostgresFDW || v.Owner() == nil || v.Owner().Extensions["postgres_fdw"] != StockPostgresFDW || len(installation.Members) != 3 {
		return InvalidProviderDescriptor()
	}
	for slot := range 2 {
		a := installation.Members[slot]
		canonical, ok := installation.Declarations[slot].(*catalog.Procedure)
		name, _, _, _ := postgresFDWDeclaration(slot)
		if !ok || a.ClassID != ids.ProcedureClassID || a.SubID != 0 || !postgresFDWProcedureShape(canonical, slot) || canonical.Name() != name || canonical.Address() != a || canonical.Origin != nil {
			return InvalidProviderDescriptor()
		}
		if d := v.Get(a); d != nil {
			live, ok := d.(*catalog.Procedure)
			if !ok || !postgresFDWProcedureShape(live, slot) || live.Revision() == 0 || !live.Origin.Valid(a) || live.Origin.Installation != installation || live.Origin.Declaration != slot || live.Owner.ID() == 0 || live.Owner != v.Owner().Principals.Principal(live.Owner.ID()) || v.NameAddress(catalog.DescriptorNameKey(live)) != a {
				return InvalidProviderDescriptor()
			}
			if _, exists := v.Namespace(live.Namespace()); !exists {
				return InvalidProviderDescriptor()
			}
			if err := catalog.ValidateExtensionMembership(v, live); err != nil {
				return err
			}
		}
	}
	canonical, ok := installation.Declarations[2].(*catalog.ForeignObject)
	a := installation.Members[2]
	if !ok || canonical == nil || a.ClassID != ids.ForeignWrapperClassID || a.SubID != 0 || canonical.Address() != a || canonical.Name() != "postgres_fdw" || canonical.Origin != nil || !postgresFDWWrapperShape(canonical, installation) {
		return InvalidProviderDescriptor()
	}
	if d := v.Get(a); d != nil {
		live, ok := d.(*catalog.ForeignObject)
		if !ok || live == nil || live.Revision() == 0 || !live.Origin.Valid(a) || live.Origin.Installation != installation || live.Origin.Declaration != 2 || !postgresFDWWrapperShape(live, installation) || live.Owner.ID() == 0 || live.Owner != v.Owner().Principals.Principal(live.Owner.ID()) || v.NameAddress(catalog.DescriptorNameKey(live)) != a {
			return InvalidProviderDescriptor()
		}
		if err := catalog.ValidateExtensionMembership(v, live); err != nil {
			return err
		}
	}
	return nil
}
func postgresFDWWrapperShape(d *catalog.ForeignObject, i *catalog.Installation) bool {
	return d.Handler == i.Members[0] && d.Validator == i.Members[1] && len(d.Options) == 0 && d.Wrapper == (ids.ObjectAddress{}) && d.Server == (ids.ObjectAddress{}) && d.MappedRole == (authorization.PrincipalRef{}) && d.MappedName == "" && d.ServerType == nil && d.Version == nil
}
func postgresFDWProcedureName(v catalog.Read, d *catalog.Procedure) (string, error) {
	if d == nil || v.Get(d.Address()) != d || d.Address().ClassID != ids.ProcedureClassID || d.Revision() == 0 || !d.Origin.Valid(d.Address()) || d.Origin.Declaration > 1 {
		return "", InvalidProviderDescriptor()
	}
	if err := validatePostgresFDWRecipe(v, d.Origin.Installation); err != nil {
		return "", err
	}
	name, _, _, _ := postgresFDWDeclaration(d.Origin.Declaration)
	return name, nil
}
func (p *PostgresFDWProvider) ValidateForeign(v catalog.Read, d *catalog.ForeignObject) error {
	if p != StockPostgresFDW || d == nil || v.Get(d.Address()) != d || d.Revision() == 0 || !d.Origin.Valid(d.Address()) || d.Origin.Declaration != 2 {
		return InvalidProviderDescriptor()
	}
	return validatePostgresFDWRecipe(v, d.Origin.Installation)
}
func (p *PostgresFDWProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	members := make([]ids.ObjectAddress, 3)
	for slot := range 3 {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		if slot < 2 {
			name, signature, result, token := postgresFDWDeclaration(slot)
			a := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
			d := &catalog.Procedure{ObjectAddress: a, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Owner: owner, RoutineKind: catalog.FunctionRoutine, Signature: signature, Result: result, Strict: true, Volatility: expr.Volatile, Implementation: p, Foreign: token, Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u'}, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/postgres_fdw", HasBinary: true}}
			if stage.LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
				context := "SQL statement \"CREATE FUNCTION postgres_fdw_handler()\nRETURNS fdw_handler\nAS '$libdir/postgres_fdw'\nLANGUAGE C STRICT\"\nextension script file \"postgres_fdw--1.0.sql\", near line 6"
				if slot == 1 {
					context = "SQL statement \"CREATE FUNCTION postgres_fdw_validator(text[], oid)\nRETURNS void\nAS '$libdir/postgres_fdw'\nLANGUAGE C STRICT\"\nextension script file \"postgres_fdw--1.0.sql\", near line 11"
				}
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", name), Context: context}
			}
			if err := stage.Create(d); err != nil {
				return err
			}
			members[slot] = a
		} else {
			a := ids.ObjectAddress{ClassID: ids.ForeignWrapperClassID, ObjectID: oid}
			d := &catalog.ForeignObject{ObjectAddress: a, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "postgres_fdw", Owner: owner, Handler: members[0], Validator: members[1]}
			if stage.LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `foreign-data wrapper "postgres_fdw" already exists`, Context: "SQL statement \"CREATE FOREIGN DATA WRAPPER postgres_fdw\n  HANDLER postgres_fdw_handler\n  VALIDATOR postgres_fdw_validator\"\nextension script file \"postgres_fdw--1.0.sql\", near line 16"}
			}
			if err := stage.Create(d); err != nil {
				return err
			}
			members[slot] = a
		}
	}
	ext.Members = members
	return nil
}
