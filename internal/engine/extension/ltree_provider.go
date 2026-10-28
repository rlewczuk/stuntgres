package extension

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type LtreeProvider struct{ control catalog.ExtensionControl }

func (p *LtreeProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *LtreeProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.3"}}
}

var StockLtree = &LtreeProvider{control: catalog.ExtensionControl{Version: "1.3", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data type for hierarchical tree-like structures")}}


// Each scalar owns a fixed type/input/output triple. Keep the existing slots stable.
var ltreeRecipes = [...]struct {
	slot int
	name string
	input, output string
	constructor func(sqltype.ID, string, string) (*sqltype.Descriptor, error)
	accept func(*sqltype.Descriptor) bool
}{
	{0, "lquery", "lquery_in", "lquery_out", sqltype.Lquery, (*sqltype.Descriptor).IsLquery},
	{3, "ltree", "ltree_in", "ltree_out", sqltype.Ltree, (*sqltype.Descriptor).IsLtree},
	{6, "ltxtquery", "ltxtq_in", "ltxtq_out", sqltype.Ltxtquery, (*sqltype.Descriptor).IsLtxtquery},
}

const LtreeTypeContext = "SQL statement \"CREATE TYPE lquery (\n\tINTERNALLENGTH = -1,\n\tINPUT = lquery_in,\n\tOUTPUT = lquery_out,\n\tSTORAGE = extended\n)\"\nextension script file \"ltree--1.1.sql\", near line 315"
const LtreeInputContext = "SQL statement \"CREATE FUNCTION lquery_in(cstring)\nRETURNS lquery\nAS '$libdir/ltree'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"ltree--1.1.sql\", near line 305"

func LquerySignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *LtreeProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid ltree manifest") }
	if !currentManifest(v, ext) || p != StockLtree || ext.Name() != "ltree" || ext.Version != p.control.Version || len(ext.Members) != LtreeMemberCount { return invalid() }
	for _, recipe := range ltreeRecipes {
		slot := recipe.slot
		if ext.Members[slot].ClassID != ids.TypeClassID || ext.Members[slot+1].ClassID != ids.ProcedureClassID || ext.Members[slot+2].ClassID != ids.ProcedureClassID { return invalid() }
		base, ok := v.Type(ext.Members[slot])
		if !ok || !recipe.accept(base.SQLType) || base.Address() != ext.Members[slot] || base.Extension != ext.Address() || base.ObjectRevision == 0 || base.Array == 0 { return invalid() }
		if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil { return err }
		array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
		if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() != (slot != 6) || array.SQLType.Element() != base.SQLType || array.Namespace() != base.Namespace() || array.Owner != base.Owner || array.Extension != (ids.ObjectAddress{}) { return invalid() }
		if err := validateRegisteredType(v, array); err != nil { return err }
		for _, address := range ext.Members[slot+1:slot+3] {
			proc, ok := v.Procedure(address)
			if !ok { return invalid() }
			if _, err := ProcedureSymbol(v, proc); err != nil { return err }
		}
	}
	for index := range ltreeArrayRecipes {
		if ext.Members[LtreeArrayProcedureStart+index].ClassID != ids.ProcedureClassID || ext.Members[LtreeArrayOperatorStart+index].ClassID != ids.OperatorClassID { return invalid() }
		proc, ok := v.Procedure(ext.Members[LtreeArrayProcedureStart+index])
		if !ok { return invalid() }
		if _, err := ProcedureSymbol(v, proc); err != nil { return err }
		if err := validateLtreeArrayOperator(v, ext, index); err != nil { return err }
	}
	base,ok:=v.Type(ext.Members[3]);if !ok{return invalid()}
	if err := validateLtreeComparisons(v, ext, base); err != nil { return err }
 if err := validateLtreeHierarchy(v, ext, base); err != nil { return err }
 if err := validateLtreePaths(v, ext); err != nil { return err }
 if err := validateLtreeLquery(v, ext); err != nil { return err }
 return validateLtreeLtxtquery(v, ext, base)
}

func (p *LtreeProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if _, err := ProcedureSymbol(v, proc); err != nil { return err }
	ext, ok := v.Extension(proc.Extension)
	if !ok { return fmt.Errorf("stuntgres: missing ltree codec installation") }
	return p.ValidateExtension(v, ext)
}
