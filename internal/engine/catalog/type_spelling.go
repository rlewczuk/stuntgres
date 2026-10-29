package catalog

import (
	"strconv"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// NamedObjectVisibleAt uses the ordinary exact-name/signature definition lookup,
// shared by presentation and portable identities without an execution dependency.
func NamedObjectVisibleAt(view Read, reader DefinitionReader, path []ids.ObjectAddress, d Descriptor) bool {
	for _, namespace := range path {
		key := DescriptorNameKey(d)
		key.Namespace = namespace
		if found := DefinitionLookupAt(view, reader, key); found != (ids.ObjectAddress{}) {
			return found == d.Address()
		}
	}
	return false
}

// TypeSpelling is the canonical regtype SQL spelling. A nil reference path
// qualifies nonspecial names; builtin aliases and generated-array syntax retain
// their ordinary SQL spelling. Unknown OIDs retain numeric regtype output.
func TypeSpelling(view Read, path []ids.ObjectAddress, id ids.OID) string {
	return typeSpellingMode(view, path, id, false)
}

// QualifiedTypeSpelling additionally uses the portable pg_temp namespace alias
// required by machine object identities, without changing ordinary regtype output.
func QualifiedTypeSpelling(view Read, id ids.OID) string {
	return typeSpellingMode(view, nil, id, true)
}

func typeSpellingMode(view Read, path []ids.ObjectAddress, id ids.OID, qualified bool) string {
	if id == 0 {
		return "-"
	}
	typ, ok := view.LookupType(sqltype.ID(id))
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	if typ.IsArray() && !typ.IsCatalogVector() {
		return typeSpellingMode(view, path, ids.OID(typ.Element().ID()), qualified) + "[]"
	}
	switch typ.ID() {
	case sqltype.Bool:
		return "boolean"
	case sqltype.Int2:
		return "smallint"
	case sqltype.Int4:
		return "integer"
	case sqltype.Int8:
		return "bigint"
	case sqltype.Float4:
		return "real"
	case sqltype.Float8:
		return "double precision"
	case sqltype.BPChar:
		return "character"
	case sqltype.VarChar:
		return "character varying"
	case sqltype.Time:
		return "time without time zone"
	case sqltype.Timetz:
		return "time with time zone"
	case sqltype.Timestamp:
		return "timestamp without time zone"
	case sqltype.Timestamptz:
		return "timestamp with time zone"
	case sqltype.VarBit:
		return "bit varying"
	case sqltype.Bit:
		return "bit"
	case sqltype.Numeric:
		return "numeric"
	case sqltype.Interval:
		return "interval"
	case sqltype.JSON:
		return "json"
	}
	d := view.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
	name := Identifier(d.Name())
	if !qualified && NamedObjectVisibleAt(view, nil, path, d) {
		return name
	}
	namespace := view.MustNamespace(d.Namespace())
	schema := namespace.Name()
	if qualified && namespace.Temporary != (ids.SessionToken{}) {
		schema = "pg_temp"
	}
	return Identifier(schema) + "." + name
}
