package sqltype

// IndexClassMetadata describes released method-specific catalog identity.
// Native provider admission is separate: diagnostic-only BRIN algorithms retain
// genuine input/default/family facts without acquiring summary capability.
type IndexClassMetadata struct {
	OID, Method, Family, Input, KeyType uint32
	Name                                string
	Default                             bool
}

// Eligibility is explicit: diagnostic-known spellings (including array_ops)
// must not become advertised providers merely because their lookup has an OID.
var supportedIndexClasses = [...]struct {
	name                    string
	method, family, keyType uint32
	nondefault              bool
}{
	{"bool_ops", 403, 424, 0, false},
	{"bytea_ops", 403, 428, 0, false},
	{"char_ops", 403, 429, 0, false},
	{"name_ops", 403, 1994, 2275, false},
	{"int2_ops", 403, 1976, 0, false},
	{"int4_ops", 403, 1976, 0, false},
	{"int8_ops", 403, 1976, 0, false},
	{"text_ops", 403, 1994, 0, false},
	{"varchar_ops", 403, 1994, 0, true},
	{"bpchar_ops", 403, 426, 0, false},
	{"oid_ops", 403, 1989, 0, false},
	{"xid8_ops", 403, 5067, 0, false},
	{"tid_ops", 403, 2789, 0, false},
	{"macaddr_ops", 403, 1984, 0, false},
	{"float4_ops", 403, 1970, 0, false},
	{"float8_ops", 403, 1970, 0, false},
	{"date_ops", 403, 434, 0, false},
	{"time_ops", 403, 1996, 0, false},
	{"timestamp_ops", 403, 434, 0, false},
	{"timestamptz_ops", 403, 434, 0, false},
	{"timetz_ops", 403, 2000, 0, false},
	{"interval_ops", 403, 1982, 0, false},
	{"numeric_ops", 403, 1988, 0, false},
	{"money_ops", 403, 2099, 0, false},
	{"uuid_ops", 403, 2968, 0, false},
	{"inet_ops", 403, 1974, 0, false},
	{"jsonb_ops", 403, 4033, 0, false},
	{"text_pattern_ops", 403, 2095, 0, true},
	{"varchar_pattern_ops", 403, 2095, 0, true},
	{"bpchar_pattern_ops", 403, 2097, 0, true},
	{"enum_ops", 403, 3522, 0, false},
	{"bool_ops", 405, 2222, 0, false},
	{"bytea_ops", 405, 2223, 0, false},
	{"char_ops", 405, 431, 0, false},
	{"name_ops", 405, 1995, 0, false},
	{"int2_ops", 405, 1977, 0, false},
	{"int4_ops", 405, 1977, 0, false},
	{"int8_ops", 405, 1977, 0, false},
	{"text_ops", 405, 1995, 0, false},
	{"varchar_ops", 405, 1995, 0, true},
	{"bpchar_ops", 405, 427, 0, false},
	{"oid_ops", 405, 1990, 0, false},
	{"xid8_ops", 405, 5032, 0, false},
	{"tid_ops", 405, 2227, 0, false},
	{"xid_ops", 405, 2225, 0, false},
	{"cid_ops", 405, 2226, 0, false},
	{"float4_ops", 405, 1971, 0, false},
	{"float8_ops", 405, 1971, 0, false},
	{"date_ops", 405, 435, 0, false},
	{"time_ops", 405, 1997, 0, false},
	{"timestamp_ops", 405, 2040, 0, false},
	{"timestamptz_ops", 405, 1999, 0, false},
	{"timetz_ops", 405, 2001, 0, false},
	{"interval_ops", 405, 1983, 0, false},
	{"numeric_ops", 405, 1998, 0, false},
	{"uuid_ops", 405, 2969, 0, false},
	{"jsonb_ops", 405, 4034, 0, false},
	{"text_pattern_ops", 405, 2229, 0, true},
	{"varchar_pattern_ops", 405, 2229, 0, true},
	{"bpchar_pattern_ops", 405, 2231, 0, true},
	{"enum_ops", 405, 3523, 0, false},
	{"inet_ops", 783, 3550, 0, true},
	{"box_ops", 783, 2593, 0, false},
	{"point_ops", 783, 1029, 603, false},
	{"poly_ops", 783, 2594, 603, false},
	{"circle_ops", 783, 2595, 603, false},
	{"tsvector_ops", 783, 3655, 3642, false},
	{"tsquery_ops", 783, 3702, 20, false},
	{"range_ops", 783, 3919, 0, false},
	{"multirange_ops", 783, 6158, 3831, false},
}

func IndexClassMetadataCount() int { return len(supportedIndexClasses) + len(brinIndexClasses) }

// IndexClassMetadataAt returns a value copy, without allocating or exposing the
// immutable owner inventory. Enumeration order is not a SQL ordering promise.
func IndexClassMetadataAt(index int) (IndexClassMetadata, bool) {
	if index >= len(supportedIndexClasses) && index < IndexClassMetadataCount() {
		return brinIndexClasses[index-len(supportedIndexClasses)], true
	}
	if index < 0 || index >= len(supportedIndexClasses) {
		return IndexClassMetadata{}, false
	}
	fact := supportedIndexClasses[index]
	var class IndexClass
	var ok bool
	if fact.method == 403 {
		class, ok = LookupIndexClass(fact.name)
	} else if fact.method == 405 {
		class, ok = LookupHashIndexClass(fact.name)
	} else {
		class, ok = LookupGiSTIndexClass(fact.name)
	}
	if !ok {
		return IndexClassMetadata{}, false
	}
	input := uint32(class.input)
	if class.enum {
		input = 3500
	} // anyenum, never a concrete user enum identity.
	return IndexClassMetadata{OID: class.OID(), Method: fact.method, Family: fact.family, Input: input, KeyType: fact.keyType, Name: class.Name, Default: !fact.nondefault}, true
}

// IndexFamilyMetadata identifies an already referenced family, not a new index
// provider or SQL catalog. Released names cannot be derived from class names.
type IndexFamilyMetadata struct {
	OID, Method uint32
	Name        string
}

func IndexFamilyMetadataByOID(oid uint32) (IndexFamilyMetadata, bool) {
	for _, f := range brinIndexFamilies {
		if f.OID == oid {
			return f, true
		}
	}
	var name string
	var method uint32
	switch oid {
	case 1029:
		name, method = "point_ops", 783
	case 2593:
		name, method = "box_ops", 783
	case 2594:
		name, method = "poly_ops", 783
	case 2595:
		name, method = "circle_ops", 783
	case 3655:
		name, method = "tsvector_ops", 783
	case 3702:
		name, method = "tsquery_ops", 783
	case 6158:
		name, method = "multirange_ops", 783
	case 3550:
		name, method = "network_ops", 783
	case 3919:
		name, method = "range_ops", 783
	case 2789:
		name, method = "tid_ops", 403
	case 1984:
		name, method = "macaddr_ops", 403
	case 2227:
		name, method = "tid_ops", 405
	case 424:
		name, method = "bool_ops", 403
	case 428:
		name, method = "bytea_ops", 403
	case 429:
		name, method = "char_ops", 403
	case 1994:
		name, method = "text_ops", 403
	case 1976:
		name, method = "integer_ops", 403
	case 426:
		name, method = "bpchar_ops", 403
	case 1989:
		name, method = "oid_ops", 403
	case 5067:
		name, method = "xid8_ops", 403
	case 1970:
		name, method = "float_ops", 403
	case 434:
		name, method = "datetime_ops", 403
	case 1996:
		name, method = "time_ops", 403
	case 2000:
		name, method = "timetz_ops", 403
	case 1982:
		name, method = "interval_ops", 403
	case 1988:
		name, method = "numeric_ops", 403
	case 2099:
		name, method = "money_ops", 403
	case 2968:
		name, method = "uuid_ops", 403
	case 1974:
		name, method = "network_ops", 403
	case 4033:
		name, method = "jsonb_ops", 403
	case 2095:
		name, method = "text_pattern_ops", 403
	case 2097:
		name, method = "bpchar_pattern_ops", 403
	case 3522:
		name, method = "enum_ops", 403
	case 2222:
		name, method = "bool_ops", 405
	case 2223:
		name, method = "bytea_ops", 405
	case 431:
		name, method = "char_ops", 405
	case 1995:
		name, method = "text_ops", 405
	case 1977:
		name, method = "integer_ops", 405
	case 427:
		name, method = "bpchar_ops", 405
	case 1990:
		name, method = "oid_ops", 405
	case 5032:
		name, method = "xid8_ops", 405
	case 2225:
		name, method = "xid_ops", 405
	case 2226:
		name, method = "cid_ops", 405
	case 1971:
		name, method = "float_ops", 405
	case 435:
		name, method = "date_ops", 405
	case 1997:
		name, method = "time_ops", 405
	case 2040:
		name, method = "timestamp_ops", 405
	case 1999:
		name, method = "timestamptz_ops", 405
	case 2001:
		name, method = "timetz_ops", 405
	case 1983:
		name, method = "interval_ops", 405
	case 1998:
		name, method = "numeric_ops", 405
	case 2969:
		name, method = "uuid_ops", 405
	case 4034:
		name, method = "jsonb_ops", 405
	case 2229:
		name, method = "text_pattern_ops", 405
	case 2231:
		name, method = "bpchar_pattern_ops", 405
	case 3523:
		name, method = "enum_ops", 405
	default:
		return IndexFamilyMetadata{}, false
	}
	return IndexFamilyMetadata{OID: oid, Method: method, Name: name}, true
}
