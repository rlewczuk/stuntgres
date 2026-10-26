package sqltype

// NativeIndexRoutine is the finite released operation inventory used after
// catalog membership selection. Optional physical callbacks are not providers.
type NativeIndexRoutine struct {
	OID         uint32
	Name        string
	Left, Right uint32
	Kind        IndexRoutineKind
	Pattern     bool
	Signature   string
}
type IndexRoutineKind uint8

const (
	IndexCompareRoutine IndexRoutineKind = iota + 1
	IndexHashRoutine
	IndexExtendedHashRoutine
)

var nativeIndexRoutines = [...]NativeIndexRoutine{
	{350, "btint2cmp", 21, 21, IndexCompareRoutine, false, "21,21"},
	{351, "btint4cmp", 23, 23, IndexCompareRoutine, false, "23,23"},
	{354, "btfloat4cmp", 700, 700, IndexCompareRoutine, false, "700,700"},
	{355, "btfloat8cmp", 701, 701, IndexCompareRoutine, false, "701,701"},
	{356, "btoidcmp", 26, 26, IndexCompareRoutine, false, "26,26"},
	{358, "btcharcmp", 18, 18, IndexCompareRoutine, false, "18,18"},
	{359, "btnamecmp", 19, 19, IndexCompareRoutine, false, "19,19"},
	{360, "bttextcmp", 25, 25, IndexCompareRoutine, false, "25,25"},
	{377, "cash_cmp", 790, 790, IndexCompareRoutine, false, "790,790"},
	{400, "hashtext", 25, 0, IndexHashRoutine, false, "25"},
	{425, "hashint4extended", 23, 20, IndexExtendedHashRoutine, false, "23,20"},
	{432, "hash_numeric", 1700, 0, IndexHashRoutine, false, "1700"},
	{441, "hashint2extended", 21, 20, IndexExtendedHashRoutine, false, "21,20"},
	{442, "hashint8extended", 20, 20, IndexExtendedHashRoutine, false, "20,20"},
	{443, "hashfloat4extended", 700, 20, IndexExtendedHashRoutine, false, "700,20"},
	{444, "hashfloat8extended", 701, 20, IndexExtendedHashRoutine, false, "701,20"},
	{445, "hashoidextended", 26, 20, IndexExtendedHashRoutine, false, "26,20"},
	{446, "hashcharextended", 18, 20, IndexExtendedHashRoutine, false, "18,20"},
	{447, "hashnameextended", 19, 20, IndexExtendedHashRoutine, false, "19,20"},
	{448, "hashtextextended", 25, 20, IndexExtendedHashRoutine, false, "25,20"},
	{449, "hashint2", 21, 0, IndexHashRoutine, false, "21"},
	{450, "hashint4", 23, 0, IndexHashRoutine, false, "23"},
	{451, "hashfloat4", 700, 0, IndexHashRoutine, false, "700"},
	{452, "hashfloat8", 701, 0, IndexHashRoutine, false, "701"},
	{453, "hashoid", 26, 0, IndexHashRoutine, false, "26"},
	{454, "hashchar", 18, 0, IndexHashRoutine, false, "18"},
	{455, "hashname", 19, 0, IndexHashRoutine, false, "19"},
	{780, "hash_numeric_extended", 1700, 20, IndexExtendedHashRoutine, false, "1700,20"},
	{836, "macaddr_cmp", 829, 829, IndexCompareRoutine, false, "829,829"},
	{842, "btint8cmp", 20, 20, IndexCompareRoutine, false, "20,20"},
	{926, "network_cmp", 869, 869, IndexCompareRoutine, false, "869,869"},
	{949, "hashint8", 20, 0, IndexHashRoutine, false, "20"},
	{972, "hashbpcharextended", 1042, 20, IndexExtendedHashRoutine, false, "1042,20"},
	{1078, "bpcharcmp", 1042, 1042, IndexCompareRoutine, false, "1042,1042"},
	{1080, "hashbpchar", 1042, 0, IndexHashRoutine, false, "1042"},
	{1092, "date_cmp", 1082, 1082, IndexCompareRoutine, false, "1082,1082"},
	{1107, "time_cmp", 1083, 1083, IndexCompareRoutine, false, "1083,1083"},
	{1314, "timestamptz_cmp", 1184, 1184, IndexCompareRoutine, false, "1184,1184"},
	{1315, "interval_cmp", 1186, 1186, IndexCompareRoutine, false, "1186,1186"},
	{1358, "timetz_cmp", 1266, 1266, IndexCompareRoutine, false, "1266,1266"},
	{1688, "time_hash", 1083, 0, IndexHashRoutine, false, "1083"},
	{1693, "btboolcmp", 16, 16, IndexCompareRoutine, false, "16,16"},
	{1696, "timetz_hash", 1266, 0, IndexHashRoutine, false, "1266"},
	{1697, "interval_hash", 1186, 0, IndexHashRoutine, false, "1186"},
	{1769, "numeric_cmp", 1700, 1700, IndexCompareRoutine, false, "1700,1700"},
	{1954, "byteacmp", 17, 17, IndexCompareRoutine, false, "17,17"},
	{2039, "timestamp_hash", 1114, 0, IndexHashRoutine, false, "1114"},
	{2045, "timestamp_cmp", 1114, 1114, IndexCompareRoutine, false, "1114,1114"},
	{2166, "bttext_pattern_cmp", 25, 25, IndexCompareRoutine, true, "25,25"},
	{2180, "btbpchar_pattern_cmp", 1042, 1042, IndexCompareRoutine, true, "1042,1042"},
	{2233, "hashtid", 27, 0, IndexHashRoutine, false, "27"},
	{2234, "hashtidextended", 27, 20, IndexExtendedHashRoutine, false, "27,20"},
	{2794, "bttidcmp", 27, 27, IndexCompareRoutine, false, "27,27"},
	{2960, "uuid_cmp", 2950, 2950, IndexCompareRoutine, false, "2950,2950"},
	{2963, "uuid_hash", 2950, 0, IndexHashRoutine, false, "2950"},
	{3251, "pg_lsn_cmp", 3220, 3220, IndexCompareRoutine, false, "3220,3220"},
	{3409, "time_hash_extended", 1083, 20, IndexExtendedHashRoutine, false, "1083,20"},
	{3410, "timetz_hash_extended", 1266, 20, IndexExtendedHashRoutine, false, "1266,20"},
	{3411, "timestamp_hash_extended", 1114, 20, IndexExtendedHashRoutine, false, "1114,20"},
	{3412, "uuid_hash_extended", 2950, 20, IndexExtendedHashRoutine, false, "2950,20"},
	{3414, "hashenumextended", 3500, 20, IndexExtendedHashRoutine, false, "3500,20"},
	{3416, "jsonb_hash_extended", 3802, 20, IndexExtendedHashRoutine, false, "3802,20"},
	{3418, "interval_hash_extended", 1186, 20, IndexExtendedHashRoutine, false, "1186,20"},
	{3514, "enum_cmp", 3500, 3500, IndexCompareRoutine, false, "3500,3500"},
	{3515, "hashenum", 3500, 0, IndexHashRoutine, false, "3500"},
	{4044, "jsonb_cmp", 3802, 3802, IndexCompareRoutine, false, "3802,3802"},
	{4045, "jsonb_hash", 3802, 0, IndexHashRoutine, false, "3802"},
	{5096, "xid8cmp", 5069, 5069, IndexCompareRoutine, false, "5069,5069"},
	{6413, "hashbytea", 17, 0, IndexHashRoutine, false, "17"},
	{6414, "hashbyteaextended", 17, 20, IndexExtendedHashRoutine, false, "17,20"},
	{6415, "hashdate", 1082, 0, IndexHashRoutine, false, "1082"},
	{6416, "hashdateextended", 1082, 20, IndexExtendedHashRoutine, false, "1082,20"},
	{6417, "hashbool", 16, 0, IndexHashRoutine, false, "16"},
	{6418, "hashboolextended", 16, 20, IndexExtendedHashRoutine, false, "16,20"},
	{6419, "hashxid", 28, 0, IndexHashRoutine, false, "28"},
	{6420, "hashxidextended", 28, 20, IndexExtendedHashRoutine, false, "28,20"},
	{6421, "hashxid8", 5069, 0, IndexHashRoutine, false, "5069"},
	{6422, "hashxid8extended", 5069, 20, IndexExtendedHashRoutine, false, "5069,20"},
	{6423, "hashcid", 29, 0, IndexHashRoutine, false, "29"},
	{6424, "hashcidextended", 29, 20, IndexExtendedHashRoutine, false, "29,20"},
	{6425, "timestamptz_hash", 1184, 0, IndexHashRoutine, false, "1184"},
	{6426, "timestamptz_hash_extended", 1184, 20, IndexExtendedHashRoutine, false, "1184,20"},
	{246, "btnametextcmp", 19, 25, IndexCompareRoutine, false, "19,25"},
	{253, "bttextnamecmp", 25, 19, IndexCompareRoutine, false, "25,19"},
	{2188, "btint48cmp", 23, 20, IndexCompareRoutine, false, "23,20"},
	{2189, "btint84cmp", 20, 23, IndexCompareRoutine, false, "20,23"},
	{2190, "btint24cmp", 21, 23, IndexCompareRoutine, false, "21,23"},
	{2191, "btint42cmp", 23, 21, IndexCompareRoutine, false, "23,21"},
	{2192, "btint28cmp", 21, 20, IndexCompareRoutine, false, "21,20"},
	{2193, "btint82cmp", 20, 21, IndexCompareRoutine, false, "20,21"},
	{2194, "btfloat48cmp", 700, 701, IndexCompareRoutine, false, "700,701"},
	{2195, "btfloat84cmp", 701, 700, IndexCompareRoutine, false, "701,700"},
	{2344, "date_cmp_timestamp", 1082, 1114, IndexCompareRoutine, false, "1082,1114"},
	{2357, "date_cmp_timestamptz", 1082, 1184, IndexCompareRoutine, false, "1082,1184"},
	{2370, "timestamp_cmp_date", 1114, 1082, IndexCompareRoutine, false, "1114,1082"},
	{2383, "timestamptz_cmp_date", 1184, 1082, IndexCompareRoutine, false, "1184,1082"},
	{2526, "timestamp_cmp_timestamptz", 1114, 1184, IndexCompareRoutine, false, "1114,1184"},
	{2533, "timestamptz_cmp_timestamp", 1184, 1114, IndexCompareRoutine, false, "1184,1114"},
	{181, "int4mi", 23, 23, IndexCompareRoutine, false, "23,23"},
}

func NativeIndexRoutineCount() int { return len(nativeIndexRoutines) }
func NativeIndexRoutineAt(i int) (NativeIndexRoutine, bool) {
	if i < 0 || i >= len(nativeIndexRoutines) {
		return NativeIndexRoutine{}, false
	}
	return nativeIndexRoutines[i], true
}
func NativeIndexRoutineByOID(oid uint32) (NativeIndexRoutine, bool) {
	for _, n := range nativeIndexRoutines {
		if n.OID == oid {
			return n, true
		}
	}
	return NativeIndexRoutine{}, false
}
