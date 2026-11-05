package extension

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type IntarrayProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *IntarrayProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *IntarrayProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.5"}}
}

var StockIntarray = &IntarrayProvider{control: catalog.ExtensionControl{Version: "1.5", Trusted: true, Superuser: true, Relocatable: true, Comment: new("functions, operators, and index support for 1-D arrays of integers")}}

const IntarrayTypeContext = "SQL statement \"CREATE TYPE query_int (\n\tINTERNALLENGTH = -1,\n\tINPUT = bqarr_in,\n\tOUTPUT = bqarr_out\n)\"\nextension script file \"intarray--1.2.sql\", near line 21"
const IntarrayInputContext = "SQL statement \"CREATE FUNCTION bqarr_in(cstring)\nRETURNS query_int\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 11"

func QueryIntSignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *IntarrayProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intarray manifest") }
	if !currentManifest(v, ext) {
		return invalid()
	}
	if p != StockIntarray || ext.Name() != "intarray" || ext.Version != p.control.Version || len(ext.Members) != IntarrayMemberCount {
		return invalid()
	}
	for group := range 2 {
		slot := group * 3
		if ext.Members[slot].ClassID != ids.TypeClassID || ext.Members[slot+1].ClassID != ids.ProcedureClassID || ext.Members[slot+2].ClassID != ids.ProcedureClassID {
			return invalid()
		}
		base, ok := v.Type(ext.Members[slot])
		if !ok || base.SQLType == nil || base.Address() != ext.Members[slot] || group == 0 && !base.SQLType.IsQueryInt() || group == 1 && !base.SQLType.IsIntbigGkey() || base.Extension != ext.Address() || base.ObjectRevision == 0 || base.Array == 0 {
			return invalid()
		}
		array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
		if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() != (group == 1) || array.SQLType.Element() != base.SQLType || array.Namespace() != base.Namespace() || array.Owner != base.Owner || array.Extension != (ids.ObjectAddress{}) {
			return invalid()
		}
		if err := validateRegisteredType(v, array); err != nil {
			return err
		}
		for _, address := range ext.Members[slot+1 : slot+3] {
			proc, ok := v.Procedure(address)
			if !ok {
				return invalid()
			}
			if _, err := ProcedureSymbol(v, proc); err != nil {
				return err
			}
		}
	}
	for slot := IntarraySetProcedureStart; slot < IntarraySetOperatorStart; slot++ {
		if ext.Members[slot].ClassID != ids.ProcedureClassID {
			return invalid()
		}
		proc, ok := v.Procedure(ext.Members[slot])
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	for i, recipe := range intarraySetRecipes {
		address := ext.Members[IntarraySetOperatorStart+i]
		op, ok := v.Operator(address)
		if !ok || address.ClassID != ids.OperatorClassID || op.Function != ext.Members[IntarraySetProcedureStart+i].ObjectID || op.Left != 1007 || op.Right != recipe.right || op.Result != recipe.result {
			return invalid()
		}
		if !catalog.SameInstallation(op, ext.Installation) {
			return invalid()
		}
		if err := ValidateProviderHeader(v, op, ids.OperatorClassID); err != nil {
			return err
		}
		if err := catalog.ValidateInstalledClassOrigin(v, op); err != nil {
			return err
		}
		canonical, ok := ext.Installation.Declarations[IntarraySetOperatorStart+i].(*catalog.Operator)
		if !ok || canonical.Name() != recipe.operator {
			return invalid()
		}
		var commutator, restriction, join ids.OID
		if recipe.commutator >= 0 {
			commutator = ext.Members[IntarraySetOperatorStart+recipe.commutator].ObjectID
		}
		if i < 3 {
			restriction = ext.Members[IntarrayEstimatorStart+i].ObjectID
			join = ext.Members[IntarrayEstimatorStart+3+i].ObjectID
		}
		if op.Commutator != commutator || op.Restriction != restriction || op.Join != join || op.Negator != 0 || op.Hash || op.Merge {
			return invalid()
		}
	}
	if err := p.validateSlice(v, ext); err != nil {
		return err
	}
	return p.validateSort(v, ext)
}

func (p *IntarrayProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}
