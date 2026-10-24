package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// InstalledType resolves a recipe role from its validated manifest, never from
// a mutable type spelling or its current namespace. Other extensions have no
// type role and return nil without error.
func InstalledType(v catalog.Read, ext *catalog.Extension) (string, *catalog.Type, error) {
	name := ""
	var err error
	switch ext.Name() {
	case "cube":
		name = "cube"
		err = StockCube.ValidateExtension(v, ext)
	case "intarray":
		name = "query_int"
		err = StockIntarray.ValidateExtension(v, ext)
	case "citext":
		name = "citext"
		err = StockCitext.ValidateExtension(v, ext)
	case "isn":
		name = "ean13"
		err = StockIsn.ValidateExtension(v, ext)
	case "earthdistance":
		name = "earth"
		err = StockEarthdistance.ValidateExtension(v, ext)
	case "lo":
		name = "lo"
		err = StockLo.ValidateExtension(v, ext)
	default:
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	t, ok := v.Type(ext.Members[0])
	if !ok || t.Extension != ext.Address() {
		return "", nil, fmt.Errorf("stuntgres: missing installed type role")
	}
	return name, t, nil
}

func MemberTypeRole(v catalog.Read, ext *catalog.Extension, t *catalog.Type) (string, error) {
	name, base, err := InstalledType(v, ext)
	if err != nil {
		return "", err
	}
	if base != nil {
		if t.Address() == base.Address() {
			return name, nil
		}
		if (ext.Name() == "earthdistance" || ext.Name() == "lo") && t.Address() == (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)}) {
			return name + "[]", nil
		}
	}
	return "", fmt.Errorf("stuntgres: invalid installed type ownership role")
}
