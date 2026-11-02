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

type PgFreespaceProvider struct{ control catalog.ExtensionControl }

var StockPgFreespace = &PgFreespaceProvider{control: catalog.ExtensionControl{Version: "1.3", Superuser: true, Relocatable: true, Comment: new("examine the free space map (FSM)")}}

func (p *PgFreespaceProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*PgFreespaceProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.3"}}
}
func PgFreespaceProviderOf(p *catalog.Procedure) *PgFreespaceProvider {
	if p == nil {
		return nil
	}
	provider, _ := p.Implementation.(*PgFreespaceProvider)
	return provider
}

// Installation failures describe the released 1.1 base script, not the final
// 1.3 declaration body produced by its subsequent updates.
const pgFreespaceScalarInstallContext = "SQL statement \"CREATE FUNCTION pg_freespace(regclass, bigint)\nRETURNS int2\nAS '$libdir/pg_freespacemap', 'pg_freespace'\nLANGUAGE C STRICT PARALLEL SAFE\"\nextension script file \"pg_freespacemap--1.1.sql\", near line 7"
const pgFreespaceWrapperInstallContext = "SQL statement \"CREATE FUNCTION\n  pg_freespace(rel regclass, blkno OUT bigint, avail OUT int2)\nRETURNS SETOF RECORD\nAS $$\n  SELECT blkno, pg_freespace($1, blkno) AS avail\n  FROM generate_series(0, pg_relation_size($1) / current_setting('block_size')::bigint - 1) AS blkno;\n$$\nLANGUAGE SQL PARALLEL SAFE\"\nextension script file \"pg_freespacemap--1.1.sql\", near line 13"

var PgFreespaceMembers = [...]struct {
	Signature      catalog.ProcedureSignature
	Result         ids.OID
	Kind           functions.PgFreespaceKind
	Declaration    catalog.InstalledRoutineDeclaration
	InstallContext string
}{
	{"2205,20", 21, functions.FreespaceBlock, catalog.InstalledRoutineDeclaration{Parallel: 's'}, pgFreespaceScalarInstallContext},
	{"2205", 2249, functions.FreespaceRelation, catalog.InstalledRoutineDeclaration{Parallel: 's', ReturnsSet: true, Rows: 1000, Outputs: catalog.NewRoutineOutputs([]ids.OID{20, 21}, "rel", "blkno", "avail")}, pgFreespaceWrapperInstallContext},
}

func (p *PgFreespaceProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	if p != StockPgFreespace || v.Owner() == nil || !currentManifest(v, ext) || ext.Name() != "pg_freespacemap" || ext.Version != "1.3" || len(ext.Members) != 2 {
		return fmt.Errorf("stuntgres: invalid pg_freespacemap manifest")
	}
	for _, a := range ext.Members {
		member, ok := v.Procedure(a)
		if !ok {
			return fmt.Errorf("stuntgres: missing pg_freespace member")
		}
		if _, err := ProcedureSymbol(v, member); err != nil {
			return err
		}
	}
	return nil
}
func (p *PgFreespaceProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *PgFreespaceProvider) ValidateProcedure(v catalog.Read, r *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, r, p)
}
func ValidatePgFreespaceIdentity(v catalog.Read, p *catalog.Procedure, ref ids.ObjectRef) error {
	current, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || p == nil || current != p || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || PgFreespaceProviderOf(p) != StockPgFreespace {
		return fmt.Errorf("stuntgres: invalid stored pg_freespace identity")
	}
	return StockPgFreespace.ValidateProcedure(v, p)
}
func (p *PgFreespaceProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	roles := stage.Roles()
	scan := stage.CurrentRead().Owner().Principals.Principal(authorization.StatScanTablesRoleID)
	if _, err := roles.Descriptor(scan); err != nil {
		return err
	}
	for slot, m := range PgFreespaceMembers {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		p := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "pg_freespace", Signature: m.Signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: m.Result, Volatility: expr.Volatile, Strict: slot == 0, Installed: m.Declaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "pg_freespace", Binary: "$libdir/pg_freespacemap", HasBinary: true}, Implementation: p}
		if slot == 1 {
			p.Display = catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage}
			p.DeclarationBody = pgFreespaceBody
		}
		if stage.LookupName(catalog.DescriptorNameKey(p)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", p.Name()), Context: m.InstallContext}
		}
		p.ACL, err = stage.RoutineCreationACLWithoutPublic(owner, ext.TargetNamespace)
		if err != nil {
			return err
		}
		p.ACL, _, err = authorization.ChangeObjectACL(context.Background(), roles, p.ACL, owner, owner, []authorization.ACLGrantee{{Role: scan}}, authorization.ExecutePrivilege, false, false, false, false)
		if err != nil {
			return err
		}
		if err = stage.Create(p); err != nil {
			return err
		}
		stage.RecordInitialPrivilege(address, 'e', p.ACL)
		ext.Members = append(ext.Members, address)
	}
	return nil
}
func ValidatePgFreespaceCall(v catalog.Read, p *catalog.Procedure, call functions.PgFreespace) error {
	if p == nil || PgFreespaceProviderOf(p) != StockPgFreespace || call.RoutineReference != p {
		return fmt.Errorf("stuntgres: invalid pg_freespace call identity")
	}
	if _, err := ProcedureSymbol(v, p); err != nil {
		return err
	}
	m := PgFreespaceMembers[p.Origin.Declaration]
	if call.Kind != m.Kind {
		return fmt.Errorf("stuntgres: mismatched pg_freespace overload")
	}
	if err := call.ValidateArguments(call.Args); err != nil {
		return err
	}
	d := call.ResultUsage().Type
	if !call.Kind.Set() {
		typ, ok := v.LookupType(sqltype.Int2)
		if !ok || call.ResultUsage() != typ.Usage() {
			return fmt.Errorf("stuntgres: invalid pg_freespace scalar")
		}
		return nil
	}
	outputs := p.OutputDeclaration()
	if d == nil || outputs == nil || d.CompositeLen() != outputs.Len() {
		return fmt.Errorf("stuntgres: invalid pg_freespace record")
	}
	for i := range outputs.Len() {
		field := d.CompositeMember(i)
		typ, ok := v.LookupType(sqltype.ID(outputs.Type(i)))
		if !ok || field.Attribute != int32(i+1) || field.Name != outputs.Name(outputs.Inputs()+i) || field.Usage != typ.Usage() {
			return fmt.Errorf("stuntgres: invalid pg_freespace OUT field")
		}
	}
	return nil
}

// PgFreespaceScalar resolves only the authenticated body's actual member slot.
func PgFreespaceScalar(v catalog.Read, wrapper *catalog.Procedure) (*catalog.Procedure, error) {
	if _, err := ProcedureSymbol(v, wrapper); err != nil {
		return nil, err
	}
	if wrapper.Implementation != StockPgFreespace || wrapper.Origin.Declaration != 1 {
		return nil, fmt.Errorf("stuntgres: invalid pg_freespace wrapper")
	}
	p, ok := v.Procedure(wrapper.Origin.Installation.Members[0])
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	if _, err := ProcedureSymbol(v, p); err != nil {
		return nil, err
	}
	if p.Implementation != StockPgFreespace || p.Origin.Declaration != 0 || p.Origin.Installation.Cohort != wrapper.Origin.Installation.Cohort {
		return nil, fmt.Errorf("stuntgres: invalid pg_freespace scalar member")
	}
	return p, nil
}
func (p *PgFreespaceProvider) VisitProcedureDependencies(_ catalog.Read, r *catalog.Procedure, yield func(ids.ObjectAddress) bool) bool {
	if p != StockPgFreespace || r == nil || r.Implementation != p || !r.Origin.Valid(r.Address()) || r.Origin.Installation.Implementation != p || len(r.Origin.Installation.Members) != 2 || r.Origin.Declaration != 1 || r.DeclarationBody != pgFreespaceBody {
		return true
	}
	return yield(r.Origin.Installation.Members[0])
}
