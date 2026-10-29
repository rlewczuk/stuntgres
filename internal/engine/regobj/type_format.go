package regobj

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// FormatType describes a logical catalog identity and a raw PostgreSQL typmod.
// specified distinguishes SQL NULL from an explicitly supplied negative typmod;
// declaration validation and regtype output deliberately have other semantics.
func FormatType(view catalog.Read, path []ids.ObjectAddress, id sqltype.ID, typmod int32, specified bool) (string, error) {
	return formatType(view, path, id, typmod, specified, regclassIdentifier)
}

// FormatDeclarationType omits typmods and shares format_type's canonical type
// spellings, while allowing a captured quote_all_identifiers policy.
func FormatDeclarationType(view catalog.Read, path []ids.ObjectAddress, id sqltype.ID, identifier func(string) string) (string, error) {
	return formatType(view, path, id, -1, false, identifier)
}

func formatType(view catalog.Read, path []ids.ObjectAddress, id sqltype.ID, typmod int32, specified bool, identifier func(string) string) (string, error) {
	if id == 0 {
		return "-", nil
	}
	typ, ok := view.LookupType(id)
	if !ok {
		return "???", nil
	}
	if typ.IsArray() && !typ.IsCatalogVector() {
		name, err := formatType(view, path, typ.Element().ID(), typmod, specified, identifier)
		if err != nil {
			return "", err
		}
		return name + "[]", nil
	}
	withTypmod := specified && typmod >= 0
	var name string
	switch id {
	case sqltype.Bool:
		return "boolean", nil
	case sqltype.Int2:
		return "smallint", nil
	case sqltype.Int4:
		return "integer", nil
	case sqltype.Int8:
		return "bigint", nil
	case sqltype.Float4:
		return "real", nil
	case sqltype.Float8:
		return "double precision", nil
	case sqltype.JSON:
		return "json", nil
	case sqltype.BPChar:
		if !specified || withTypmod {
			name = "character"
		}
	case sqltype.Bit:
		if !specified || withTypmod {
			name = "bit"
		}
	case sqltype.VarChar:
		name = "character varying"
	case sqltype.VarBit:
		name = "bit varying"
	case sqltype.Numeric:
		name = "numeric"
	case sqltype.Interval:
		name = "interval"
	case sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz:
		name = "time"
		if id == sqltype.Timestamp || id == sqltype.Timestamptz {
			name = "timestamp"
		}
		if withTypmod {
			name += rawTypeModifier(typmod)
		}
		if id == sqltype.Timetz || id == sqltype.Timestamptz {
			return name + " with time zone", nil
		}
		return name + " without time zone", nil
	}
	if name == "" {
		d := view.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
		name = identifier(d.Name())
		if !NamedObjectVisible(view, path, d) {
			namespace := view.MustNamespace(d.Namespace())
			schema := namespace.Name()
			if namespace.Temporary != (ids.SessionToken{}) {
				schema = "pg_temp"
			}
			name = identifier(schema) + "." + name
		}
	}
	if !withTypmod {
		return name, nil
	}
	switch id {
	case sqltype.BPChar, sqltype.VarChar:
		if typmod > 4 {
			name += rawTypeModifier(typmod - 4)
		}
	case sqltype.Numeric:
		if typmod >= 4 {
			packed := typmod - 4
			precision := (packed >> 16) & 65535
			scale := ((packed & 2047) ^ 1024) - 1024
			name += "(" + strconv.FormatInt(int64(precision), 10) + "," + strconv.FormatInt(int64(scale), 10) + ")"
		}
	case sqltype.Interval:
		suffix, err := rawIntervalModifier(typmod)
		if err != nil {
			return "", err
		}
		name += suffix
	default:
		name += rawTypeModifier(typmod)
	}
	return name, nil
}

func rawTypeModifier(typmod int32) string {
	return "(" + strconv.FormatInt(int64(typmod), 10) + ")"
}

func rawIntervalModifier(typmod int32) (string, error) {
	var fields string
	switch (typmod >> 16) & 32767 {
	case 4:
		fields = " year"
	case 2:
		fields = " month"
	case 8:
		fields = " day"
	case 1024:
		fields = " hour"
	case 2048:
		fields = " minute"
	case 4096:
		fields = " second"
	case 6:
		fields = " year to month"
	case 1032:
		fields = " day to hour"
	case 3080:
		fields = " day to minute"
	case 7176:
		fields = " day to second"
	case 3072:
		fields = " hour to minute"
	case 7168:
		fields = " hour to second"
	case 6144:
		fields = " minute to second"
	case 32767:
	default:
		return "", sqlError("XX000", fmt.Sprintf("invalid INTERVAL typmod: 0x%x", typmod))
	}
	precision := typmod & 65535
	if precision != 65535 {
		fields += rawTypeModifier(precision)
	}
	return fields, nil
}

// BaseType follows immediate domain IDs through the captured catalog. Physical
// descriptor chains can outlive a catalog entry and are not lookup authority.
func BaseType(view catalog.Read, id sqltype.ID) (sqltype.ID, bool) {
	for {
		typ, ok := view.LookupType(id)
		if !ok {
			return 0, false
		}
		if !typ.IsDomain() {
			return id, true
		}
		id = typ.DomainBase().Type.ID()
	}
}
