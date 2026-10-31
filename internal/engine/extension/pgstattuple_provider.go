package extension

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type PgstattupleProvider struct{ control catalog.ExtensionControl }

var StockPgstattuple = &PgstattupleProvider{control: catalog.ExtensionControl{Version: "1.5", Superuser: true, Relocatable: true, Comment: new("show tuple-level statistics")}}

func (p *PgstattupleProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*PgstattupleProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.5"}}
}
func PgstattupleProviderOf(p *catalog.Procedure) *PgstattupleProvider {
	if p == nil {
		return nil
	}
	v, _ := p.Implementation.(*PgstattupleProvider)
	return v
}

var pgstatindexOutputs = catalog.NewRoutineOutputs([]ids.OID{23, 23, 20, 20, 20, 20, 20, 20, 701, 701}, "relname", "version", "tree_level", "index_size", "root_block_no", "internal_pages", "leaf_pages", "empty_pages", "deleted_pages", "avg_leaf_density", "leaf_fragmentation")
var PgstattupleMembers = [...]struct {
	Signature catalog.ProcedureSignature
	Source    string
}{{"25", "pgstatindex_v1_5"}, {"2205", "pgstatindexbyid_v1_5"}}

func pgstatindexDeclaration() catalog.InstalledRoutineDeclaration {
	return catalog.InstalledRoutineDeclaration{Parallel: 's', Outputs: pgstatindexOutputs}
}
func (p *PgstattupleProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	if p != StockPgstattuple || v.Owner() == nil || !currentManifest(v, ext) || ext.Name() != "pgstattuple" || ext.Version != "1.5" || len(ext.Members) != len(PgstattupleMembers) {
		return fmt.Errorf("stuntgres: invalid pgstattuple manifest")
	}
	for _, a := range ext.Members {
		proc, ok := v.Procedure(a)
		if !ok {
			return fmt.Errorf("stuntgres: missing pgstattuple member")
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
}
func (p *PgstattupleProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	return ValidateCurrentInstallation(v, ext, p)
}
func (p *PgstattupleProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}
func ValidatePgstatindexIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || proc == nil || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || PgstattupleProviderOf(proc) != StockPgstattuple {
		return fmt.Errorf("stuntgres: invalid stored pgstatindex identity")
	}
	return StockPgstattuple.ValidateProcedure(v, proc)
}
func (p *PgstattupleProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	roles := stage.Roles()
	scan := stage.CurrentRead().Owner().Principals.Principal(authorization.StatScanTablesRoleID)
	if _, err := roles.Descriptor(scan); err != nil {
		return err
	}
	for _, m := range PgstattupleMembers {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "pgstatindex", Signature: m.Signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2249, Volatility: expr.Volatile, Strict: true, Installed: pgstatindexDeclaration(), Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: m.Source, Binary: "$libdir/pgstattuple", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		proc.ACL, err = stage.RoutineCreationACLWithoutPublic(owner, ext.TargetNamespace)
		if err != nil {
			return err
		}
		proc.ACL, _, err = authorization.ChangeObjectACL(context.Background(), roles, proc.ACL, owner, owner, []authorization.ACLGrantee{{Role: scan}}, authorization.ExecutePrivilege, false, false, false, false)
		if err != nil {
			return err
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
		stage.RecordInitialPrivilege(address, 'e', proc.ACL)
		ext.Members = append(ext.Members, address)
	}
	return nil
}
func ValidatePgstatindexCall(v catalog.Read, p *catalog.Procedure, call functions.Pgstatindex) error {
	if p == nil || PgstattupleProviderOf(p) != StockPgstattuple || call.RoutineReference != p {
		return fmt.Errorf("stuntgres: invalid pgstatindex call identity")
	}
	symbol, err := ProcedureSymbol(v, p)
	if err != nil {
		return err
	}
	if symbol != "pgstatindex" || !((call.Input == sqltype.Text && p.Signature == "25") || (call.Input == sqltype.Regclass && p.Signature == "2205")) {
		return fmt.Errorf("stuntgres: mismatched pgstatindex overload")
	}
	if err := call.ValidateArguments(call.Args); err != nil {
		return err
	}
	d := call.ResultUsage().Type
	outputs := p.OutputDeclaration()
	if d == nil || outputs == nil || d.CompositeLen() != outputs.Len() {
		return fmt.Errorf("stuntgres: invalid pgstatindex record")
	}
	for i := range outputs.Len() {
		m := d.CompositeMember(i)
		typ, ok := v.LookupType(sqltype.ID(outputs.Type(i)))
		if !ok || m.Attribute != int32(i+1) || m.Name != outputs.Name(outputs.Inputs()+i) || m.Usage != typ.Usage() {
			return fmt.Errorf("stuntgres: invalid pgstatindex OUT field")
		}
	}
	return nil
}
