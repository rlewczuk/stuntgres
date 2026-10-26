package extension

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type LtreeProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *LtreeProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *LtreeProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.3"}}
}

var StockLtree = &LtreeProvider{control: catalog.ExtensionControl{Version: "1.3", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data type for hierarchical tree-like structures")}}

const LtreeTypeContext = "SQL statement \"CREATE TYPE lquery (\n\tINTERNALLENGTH = -1,\n\tINPUT = lquery_in,\n\tOUTPUT = lquery_out,\n\tSTORAGE = extended\n)\"\nextension script file \"ltree--1.1.sql\", near line 315"
const LtreeInputContext = "SQL statement \"CREATE FUNCTION lquery_in(cstring)\nRETURNS lquery\nAS '$libdir/ltree'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"ltree--1.1.sql\", near line 305"

func LquerySignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *LtreeProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid ltree manifest") }
	if !currentManifest(v, ext) {
		return invalid()
	}
	if p != StockLtree || ext.Name() != "ltree" || ext.Version != p.control.Version || len(ext.Members) != 3 {
		return invalid()
	}
	if ext.Members[0].ClassID != ids.TypeClassID || ext.Members[1].ClassID != ids.ProcedureClassID || ext.Members[2].ClassID != ids.ProcedureClassID {
		return invalid()
	}
	base, ok := v.Type(ext.Members[0])
	if !ok || base.SQLType == nil || base.Address() != ext.Members[0] || !base.SQLType.IsLquery() || base.Extension != ext.Address() || base.ObjectRevision == 0 || base.Array == 0 {
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

func (p *LtreeProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if _, err := ProcedureSymbol(v, proc); err != nil {
		return err
	}
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing ltree codec installation")
	}
	return p.ValidateExtension(v, ext)
}
