package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// InstalledTypeRole pairs a canonical recipe key with its current descriptor.
type InstalledTypeRole struct {
	Name string
	Type *catalog.Type
}

// InstalledTypes resolves all roles from the validated manifest, not mutable names.
func InstalledTypes(v catalog.Read, ext *catalog.Extension) ([]InstalledTypeRole, error) {
	name := ""
	var err error
	switch ext.Name() {
	case "cube":
		name = "cube"
		err = StockCube.ValidateExtension(v, ext)
	case "intarray":
		name = "query_int"
		err = StockIntarray.ValidateExtension(v, ext)
	case "ltree":
		if err := StockLtree.ValidateExtension(v, ext); err != nil { return nil, err }
		roles := make([]InstalledTypeRole, len(ltreeRecipes))
		for i, recipe := range ltreeRecipes {
			roles[i] = InstalledTypeRole{recipe.name, v.MustType(ext.Members[recipe.slot])}
		}
		return roles, nil
	case "hstore":
		name = "hstore"
		err = StockHstore.ValidateExtension(v, ext)
	case "citext":
		name = "citext"
		err = StockCitext.ValidateExtension(v, ext)
	case "isn":
		if err := StockIsn.ValidateExtension(v, ext); err != nil {
			return nil, err
		}
		roles := make([]InstalledTypeRole, len(isnRecipes))
		for i, recipe := range isnRecipes {
			roles[i] = InstalledTypeRole{recipe.name, v.MustType(ext.Members[recipe.slot])}
		}
		return roles, nil
	case "earthdistance":
		name = "earth"
		err = StockEarthdistance.ValidateExtension(v, ext)
	case "lo":
		name = "lo"
		err = StockLo.ValidateExtension(v, ext)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, ok := v.Type(ext.Members[0])
	if !ok || t.Extension != ext.Address() {
		return nil, fmt.Errorf("stuntgres: missing installed type role")
	}
	return []InstalledTypeRole{{name, t}}, nil
}

func MemberTypeRole(v catalog.Read, ext *catalog.Extension, t *catalog.Type) (string, error) {
	roles, err := InstalledTypes(v, ext)
	if err != nil {
		return "", err
	}
	for _, role := range roles {
		if t.Address() == role.Type.Address() && t.SQLType == role.Type.SQLType {
			return role.Name, nil
		}
		if (ext.Name() == "earthdistance" || ext.Name() == "lo") && t.Address() == (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(role.Type.Array)}) {
			return role.Name + "[]", nil
		}
	}
	return "", fmt.Errorf("stuntgres: invalid installed type ownership role")
}
