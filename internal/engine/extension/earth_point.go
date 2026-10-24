package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const (
	EarthPointProcedureSlot = 8
	EarthPointOperatorSlot  = 9
	EarthMemberCount        = 10
)

func (p *EarthdistanceProvider) stagePoint(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	proc := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "geo_distance", Signature: "600,600", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: ids.OID(sqltype.Float8), Volatility: expr.Immutable, Strict: true, Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: `function "geo_distance" already exists with same argument types`, Context: "SQL statement \"CREATE FUNCTION geo_distance (point, point)\nRETURNS float8\nLANGUAGE C IMMUTABLE STRICT PARALLEL SAFE AS '$libdir/earthdistance'\"\nextension script file \"earthdistance--1.1.sql\", near line 87"}
	}
	ext.Members[EarthPointProcedureSlot] = proc.Address()
	if err := stage.Create(proc); err != nil {
		return err
	}
	oid, err = stage.AllocateOID()
	if err != nil {
		return err
	}
	revision, err = stage.Revision()
	if err != nil {
		return err
	}
	op := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "<@>", Left: ids.OID(sqltype.Point), Right: ids.OID(sqltype.Point), Owner: owner, Result: ids.OID(sqltype.Float8), Function: proc.Address().ObjectID, Commutator: oid}
	if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: "operator <@> already exists"}
	}
	ext.Members[EarthPointOperatorSlot] = op.Address()
	return stage.Create(op)
}

func (p *EarthdistanceProvider) validatePoint(v catalog.Read, ext *catalog.Extension) error {
	proc, ok := v.Procedure(ext.Members[EarthPointProcedureSlot])
	if !ok || EarthdistanceProviderOf(proc) != p {
		return InvalidProviderDescriptor()
	}
	if symbol, err := ProcedureSymbol(v, proc); err != nil || symbol != "geo_distance" {
		return InvalidProviderDescriptor()
	}
	op, ok := v.Operator(ext.Members[EarthPointOperatorSlot])
	if !ok || op.Address().ClassID != ids.OperatorClassID || op.Address() != ext.Members[EarthPointOperatorSlot] || op.Namespace() != ext.TargetNamespace || op.Extension != ext.Address() || op.Revision() == 0 || op.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(op)) != op.Address() || op.Name() != "<@>" || op.Left != ids.OID(sqltype.Point) || op.Right != op.Left || op.Result != ids.OID(sqltype.Float8) || op.Function != proc.Address().ObjectID || op.Commutator != op.Address().ObjectID || op.Negator != 0 || op.Restriction != 0 || op.Join != 0 || op.Hash || op.Merge {
		return fmt.Errorf("stuntgres: invalid earth point operator")
	}
	if op.Owner != v.Owner().Principals.Principal(op.Owner.ID()) {
		return InvalidProviderDescriptor()
	}
	return nil
}

// ValidateEarthPointCall checks the installed declaration and exact builtin
// descriptor generations before the kernel may apply strict NULL behavior.
func ValidateEarthPointCall(v catalog.Read, proc *catalog.Procedure, call functions.EarthPoint) error {
	if v.State() == nil || proc == nil || EarthdistanceProviderOf(proc) != StockEarthdistance || v.Get(proc.Address()) != proc || call.RoutineReference != proc {
		return InvalidProviderDescriptor()
	}
	ext, installed := v.Extension(proc.Extension)
	if !installed || ext.Name() != "earthdistance" || ext.Version != "1.2" || len(ext.Members) != EarthMemberCount || ext.Members[EarthPointProcedureSlot] != proc.Address() || !currentManifest(v, ext) {
		return InvalidProviderDescriptor()
	}
	// Whole domain/cube validation belongs to bind/import admission, not each
	// scalar evaluation. The bounded point tail still proves current links.
	if err := StockEarthdistance.validatePoint(v, ext); err != nil {
		return err
	}
	point, ok := v.LookupType(sqltype.Point)
	result, resultOK := v.LookupType(sqltype.Float8)
	if !ok || !resultOK || call.ResultUsage() != result.Usage() || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].ResultUsage() != point.Usage() || call.Args[1].ResultUsage() != point.Usage() {
		return InvalidProviderDescriptor()
	}
	return call.Validate()
}
