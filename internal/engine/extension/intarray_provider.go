package extension

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
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

func (p *IntarrayProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intarray manifest") }
	if p != StockIntarray || ext.Name() != "intarray" || ext.Version != p.control.Version || len(ext.Members) != 3 {
		return invalid()
	}
	if ext.Members[0].ClassID != ids.TypeClassID || ext.Members[1].ClassID != ids.ProcedureClassID || ext.Members[2].ClassID != ids.ProcedureClassID {
		return invalid()
	}
	base, ok := v.Type(ext.Members[0])
	if !ok || base.SQLType == nil || base.Address() != ext.Members[0] || !base.SQLType.IsQueryInt() || base.Name() != "query_int" || base.Extension != ext.Address() || base.Namespace() != ext.TargetNamespace || base.ObjectRevision == 0 || base.Array == 0 {
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
		if !ok || proc.Address() != address || IntarrayProviderOf(proc) != p || proc.Extension != ext.Address() || proc.Namespace() != ext.TargetNamespace || proc.Revision() == 0 {
			return invalid()
		}
		name, signature, result := "bqarr_in", catalog.ProcedureSignature("2275"), ids.OID(base.SQLType.ID())
		if i == 1 {
			name, signature, result = "bqarr_out", QueryIntSignature(base.SQLType.ID()), 2275
		}
		if proc.Name() != name || proc.Signature != signature || proc.Result != result || !proc.Strict || proc.Volatility != expr.Immutable || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) {
			return invalid()
		}
	}
	return nil
}

func (p *IntarrayProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing intarray codec installation")
	}
	return p.ValidateExtension(v, ext)
}
