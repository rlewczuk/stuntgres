package inquiry

import (
	"fmt"

	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// Type inquiries borrow the execution frontier's coherent catalog and authorized
// relation/type path, including TEMP. Evaluation never consults live settings.
func FormatType(h CatalogHost, oid uint32, typmod int32, specified bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	read, relations, _ := h.VisibilitySnapshot()
	if read.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing type inquiry snapshot")
	}
	text, err := regobj.FormatType(read, relations, sqltype.ID(oid), typmod, specified)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, text), nil
}

func BaseType(h CatalogHost, oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	read, _, _ := h.VisibilitySnapshot()
	if read.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing type inquiry snapshot")
	}
	id, found := regobj.BaseType(read, sqltype.ID(oid))
	if !found {
		return sqltype.Null(result), nil
	}
	return sqltype.RegtypeValue(result, uint32(id))
}
