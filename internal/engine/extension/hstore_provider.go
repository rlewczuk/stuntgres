package extension

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type HstoreProvider struct{ control catalog.ExtensionControl }

var StockHstore = &HstoreProvider{control: catalog.ExtensionControl{Version: "1.8", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data type for storing sets of (key, value) pairs")}}

func (p *HstoreProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *HstoreProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.8"}}
}

func HstoreSignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *HstoreProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid hstore manifest") }
	if !currentManifest(v, ext) || p != StockHstore || ext.Name() != "hstore" || ext.Version != p.control.Version || len(ext.Members) != 3 {
		return invalid()
	}
	if ext.Members[0].ClassID != ids.TypeClassID || ext.Members[1].ClassID != ids.ProcedureClassID || ext.Members[2].ClassID != ids.ProcedureClassID {
		return invalid()
	}
	base, ok := v.Type(ext.Members[0])
	if !ok || base.SQLType == nil || base.Address() != ext.Members[0] || !base.SQLType.IsHstore() || base.Extension != ext.Address() || base.ObjectRevision == 0 || base.Array == 0 {
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

func (p *HstoreProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if _, err := ProcedureSymbol(v, proc); err != nil {
		return err
	}
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing hstore codec installation")
	}
	return p.ValidateExtension(v, ext)
}
