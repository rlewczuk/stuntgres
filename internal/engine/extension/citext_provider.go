package extension

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type CitextProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *CitextProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *CitextProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.8"}}
}

var StockCitext = &CitextProvider{control: catalog.ExtensionControl{Version: "1.8", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data type for case-insensitive character strings")}}

const CitextTypeContext = "SQL statement \"CREATE TYPE citext\"\nextension script file \"citext--1.4.sql\", near line 17"
const CitextInputContext = "SQL statement \"CREATE FUNCTION citextin(cstring)\nRETURNS citext\nAS 'textin'\nLANGUAGE internal IMMUTABLE STRICT PARALLEL SAFE\"\nextension script file \"citext--1.4.sql\", near line 22"

func CitextSignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *CitextProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid citext manifest") }
	if !currentManifest(v, ext) {
		return invalid()
	}
	if p != StockCitext || ext.Name() != "citext" || ext.Version != p.control.Version || len(ext.Members) != CitextMemberCount {
		return invalid()
	}
	if ext.Members[0].ClassID != ids.TypeClassID || ext.Members[1].ClassID != ids.ProcedureClassID || ext.Members[2].ClassID != ids.ProcedureClassID {
		return invalid()
	}
	base, ok := v.Type(ext.Members[0])
	if !ok || base.SQLType == nil || base.Address() != ext.Members[0] || !base.SQLType.IsCitext() || base.Extension != ext.Address() || base.ObjectRevision == 0 || base.Array == 0 {
		return invalid()
	}
	array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
	if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() || array.SQLType.Element() != base.SQLType || array.Namespace() != base.Namespace() || array.Owner != base.Owner || array.Extension != (ids.ObjectAddress{}) {
		return invalid()
	}
	if err := validateRegisteredType(v, array); err != nil {
		return err
	}
	for slot := 1; slot < CitextMemberCount; slot++ {
		if slot >= CitextComparisonOperatorStart && slot < CitextConversionProcedureStart {
			continue
		}
		proc, ok := v.Procedure(ext.Members[slot])
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	for i := range CitextComparisonNames {
		if err := validateCitextOperator(v, ext, base, i); err != nil {
			return err
		}
	}
	return nil
}

func (p *CitextProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if _, err := ProcedureSymbol(v, proc); err != nil {
		return err
	}
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing citext codec installation")
	}
	return p.ValidateExtension(v, ext)
}
