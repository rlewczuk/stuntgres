package regobj

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// VisibilityInquiry answers the pg_*_is_visible and collation-version
// inquiries for oid over the statement's visibility snapshot: its catalog,
// the relation path and the path for collations and conversions (TEMP
// filtered). pg_import_system_collations is answered by the caller.
func VisibilityInquiry(view catalog.Read, relations, others []ids.ObjectAddress, kind functions.VisibilityInquiryKind, oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	d, exists := view.Descriptor(ids.ObjectAddress{ClassID: kind.Class(), ObjectID: ids.OID(oid)})
	if !exists {
		if kind == functions.CollationVersionInquiry || kind == functions.DatabaseCollationVersionInquiry {
			objectKind := "collation"
			if kind == functions.DatabaseCollationVersionInquiry {
				objectKind = "database"
			}
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("%s with OID %d does not exist", objectKind, oid))
		}
		return sqltype.Null(result), nil
	}
	if kind == functions.CollationVersionInquiry || kind == functions.DatabaseCollationVersionInquiry {
		var version *string
		var err error
		if kind == functions.DatabaseCollationVersionInquiry {
			version, err = pgcatalog.DatabaseCollationActualVersion()
		} else {
			version, err = pgcatalog.CollationActualVersion(d.(*catalog.Collation))
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		if version == nil {
			return sqltype.Null(result), nil
		}
		return sqltype.String(result, *version), nil
	}
	path := relations
	if kind == functions.CollationVisibility || kind == functions.ConversionVisibility {
		path = others
	}
	visible := false
	if kind == functions.CollationVisibility {
		visible = VisibleCollation(view, path, d)
	} else {
		visible = NamedObjectVisible(view, path, d)
	}
	return sqltype.Boolean(result, visible), nil
}
