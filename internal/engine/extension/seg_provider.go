package extension

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type SegProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *SegProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *SegProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.4"}}
}

var StockSeg = &SegProvider{control: catalog.ExtensionControl{Version: "1.4", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data type for representing line segments or floating-point intervals")}}

const SegTypeContext = "SQL statement \"CREATE TYPE seg (\n\tINTERNALLENGTH = 12,\n\tINPUT = seg_in,\n\tOUTPUT = seg_out\n)\"\nextension script file \"seg--1.1.sql\", near line 18"
const SegInputContext = "SQL statement \"CREATE FUNCTION seg_in(cstring)\nRETURNS seg\nAS '$libdir/seg'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"seg--1.1.sql\", near line 11"

func SegSignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *SegProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid seg manifest") }
	if !currentManifest(v, ext) {
		return invalid()
	}
	if p != StockSeg || ext.Name() != "seg" || ext.Version != p.control.Version || len(ext.Members) != 3 {
		return invalid()
	}
	if ext.Members[0].ClassID != ids.TypeClassID || ext.Members[1].ClassID != ids.ProcedureClassID || ext.Members[2].ClassID != ids.ProcedureClassID {
		return invalid()
	}
	base, ok := v.Type(ext.Members[0])
	if !ok || base.SQLType == nil || base.Address() != ext.Members[0] || !base.SQLType.IsSeg() || base.Extension != ext.Address() || base.ObjectRevision == 0 || base.Array == 0 {
		return invalid()
	}
	array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
	if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() || array.SQLType.Element() != base.SQLType || array.Namespace() != base.Namespace() || array.Owner != base.Owner || array.Extension != (ids.ObjectAddress{}) {
		return invalid()
	}
	if err := validateRegisteredType(v, array); err != nil {
		return err
	}
	for _, address := range ext.Members[1:] {
		proc, ok := v.Procedure(address)
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *SegProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}
