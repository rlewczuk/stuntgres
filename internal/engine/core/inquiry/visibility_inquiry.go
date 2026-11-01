package inquiry

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func VisibilityInquiry(h CatalogHost, kind functions.VisibilityInquiryKind, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	var oid uint32
	var err error
	if kind == functions.ImportSystemCollationsInquiry {
		oid, err = value.ObjectOID()
	} else {
		oid, err = value.OID()
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if kind == functions.ImportSystemCollationsInquiry {
		h.ReacquireEngine()
		defer h.ReleaseEngineLocked()
		return sqltype.Value{}, h.ImportSystemCollationsLocked(oid)
	}
	read, relations, others := h.VisibilitySnapshot()
	if read.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing visibility snapshot")
	}
	return regobj.VisibilityInquiry(read, relations, others, kind, oid, result)
}
