package sqltype

// IndexClassMetadata describes an implemented method-specific operator class.
// Identity and input come from the same owner that admits executable index keys.
// Family/default/storage facts are released catalog properties, not capabilities.
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
}

func IndexClassMetadataCount() int { return len(supportedIndexClasses) }

// IndexClassMetadataAt returns a value copy, without allocating or exposing the
// immutable owner inventory. Enumeration order is not a SQL ordering promise.
func IndexClassMetadataAt(index int) (IndexClassMetadata, bool) {
	if index < 0 || index >= len(supportedIndexClasses) {
		return IndexClassMetadata{}, false
	}
	fact := supportedIndexClasses[index]
	var class IndexClass
	var ok bool
	if fact.method == 403 {
		class, ok = LookupIndexClass(fact.name)
	} else {
		class, ok = LookupHashIndexClass(fact.name)
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
