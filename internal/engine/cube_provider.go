package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type CubeProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *CubeProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *CubeProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.5"}}
}

var StockCube = &CubeProvider{control: catalog.ExtensionControl{Version: "1.5", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data type for multidimensional cubes")}}

const CubeTypeContext = "SQL statement \"CREATE TYPE cube (\n\tINTERNALLENGTH = variable,\n\tINPUT = cube_in,\n\tOUTPUT = cube_out,\n\tALIGNMENT = double\n)\"\nextension script file \"cube--1.2.sql\", near line 26"
const CubeInputContext = "SQL statement \"CREATE FUNCTION cube_in(cstring)\nRETURNS cube\nAS '$libdir/cube'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"cube--1.2.sql\", near line 11"

func CubeSignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *CubeProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid cube manifest") }
	if p != StockCube || ext.Name() != "cube" || ext.Version != p.control.Version || len(ext.Members) != 3 {
		return invalid()
	}
	if ext.Members[0].ClassID != ids.TypeClassID || ext.Members[1].ClassID != ids.ProcedureClassID || ext.Members[2].ClassID != ids.ProcedureClassID {
		return invalid()
	}
	base, ok := v.Type(ext.Members[0])
	if !ok || base.SQLType == nil || base.Address() != ext.Members[0] || !base.SQLType.IsCube() || base.Name() != "cube" || base.Extension != ext.Address() || base.Namespace() != ext.TargetNamespace || base.ObjectRevision == 0 || base.Array == 0 {
		return invalid()
	}
	array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
	if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() || array.SQLType.Element() != base.SQLType || array.Namespace() != base.Namespace() || array.Owner != base.Owner || array.Extension != (ids.ObjectAddress{}) {
		return invalid()
	}
	if err := validateRegisteredType(v, array); err != nil {
		return err
	}
	for i, address := range ext.Members[1:] {
		proc, ok := v.Procedure(address)
		if !ok || proc.Address() != address || CubeProviderOf(proc) != p || proc.Extension != ext.Address() || proc.Namespace() != ext.TargetNamespace || proc.Revision() == 0 {
			return invalid()
		}
		name, signature, result := "cube_in", catalog.ProcedureSignature("2275"), ids.OID(base.SQLType.ID())
		if i == 1 {
			name, signature, result = "cube_out", CubeSignature(base.SQLType.ID()), 2275
		}
		if proc.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address {
			return invalid()
		}
		if proc.RoutineKind != catalog.FunctionRoutine || proc.Aggregate != nil || proc.Foreign != nil || proc.Builtin != nil {
			return invalid()
		}
		if proc.Name() != name || proc.Signature != signature || proc.Result != result || !proc.Strict || proc.Volatility != expr.Immutable || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) {
			return invalid()
		}
	}
	return nil
}

func (p *CubeProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing cube codec installation")
	}
	return p.ValidateExtension(v, ext)
}
