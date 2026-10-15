package reference_test

import (
	"sort"
	"strings"
)

// Feature-owned explicit REL_18_0 tuples, independently confirmed by the public
// cast/class oracle. These are expectations, not imports from native metadata or
// duplicate scenario bodies. Cast row OIDs remain backend-local relationships.
func objectAddressCastFacts() [][]any {
	return [][]any{
		{int64(21), int64(23), int64(313), "i", "f"},
		{int64(21), int64(20), int64(754), "i", "f"},
		{int64(21), int64(700), int64(236), "i", "f"},
		{int64(21), int64(701), int64(235), "i", "f"},
		{int64(21), int64(1700), int64(1782), "i", "f"},
		{int64(23), int64(21), int64(314), "a", "f"},
		{int64(23), int64(20), int64(481), "i", "f"},
		{int64(23), int64(700), int64(318), "i", "f"},
		{int64(23), int64(701), int64(316), "i", "f"},
		{int64(23), int64(1700), int64(1740), "i", "f"},
		{int64(20), int64(21), int64(714), "a", "f"},
		{int64(20), int64(23), int64(480), "a", "f"},
		{int64(20), int64(700), int64(652), "i", "f"},
		{int64(20), int64(701), int64(482), "i", "f"},
		{int64(20), int64(1700), int64(1781), "i", "f"},
		{int64(700), int64(21), int64(238), "a", "f"},
		{int64(700), int64(23), int64(319), "a", "f"},
		{int64(700), int64(20), int64(653), "a", "f"},
		{int64(700), int64(701), int64(311), "i", "f"},
		{int64(700), int64(1700), int64(1742), "a", "f"},
		{int64(701), int64(21), int64(237), "a", "f"},
		{int64(701), int64(23), int64(317), "a", "f"},
		{int64(701), int64(20), int64(483), "a", "f"},
		{int64(701), int64(700), int64(312), "a", "f"},
		{int64(701), int64(1700), int64(1743), "a", "f"},
		{int64(1700), int64(21), int64(1783), "a", "f"},
		{int64(1700), int64(23), int64(1744), "a", "f"},
		{int64(1700), int64(20), int64(1779), "a", "f"},
		{int64(1700), int64(700), int64(1745), "i", "f"},
		{int64(1700), int64(701), int64(1746), "i", "f"},
		{int64(21), int64(26), int64(313), "i", "f"},
		{int64(23), int64(26), int64(0), "i", "b"},
		{int64(20), int64(26), int64(1287), "i", "f"},
		{int64(26), int64(23), int64(0), "a", "b"},
		{int64(26), int64(20), int64(1288), "a", "f"},
		{int64(1082), int64(1114), int64(2024), "i", "f"},
		{int64(1082), int64(1184), int64(1174), "i", "f"},
		{int64(1083), int64(1266), int64(2047), "i", "f"},
		{int64(1114), int64(1082), int64(2029), "a", "f"},
		{int64(1114), int64(1083), int64(1316), "a", "f"},
		{int64(1114), int64(1184), int64(2028), "i", "f"},
		{int64(1184), int64(1082), int64(1178), "a", "f"},
		{int64(1184), int64(1083), int64(2019), "a", "f"},
		{int64(1184), int64(1114), int64(2027), "a", "f"},
		{int64(1184), int64(1266), int64(1388), "a", "f"},
		{int64(1266), int64(1083), int64(2046), "a", "f"},
		{int64(25), int64(1042), int64(0), "i", "b"},
		{int64(25), int64(1043), int64(0), "i", "b"},
		{int64(1042), int64(25), int64(401), "i", "f"},
		{int64(1042), int64(1043), int64(401), "i", "f"},
		{int64(1043), int64(25), int64(0), "i", "b"},
		{int64(1043), int64(1042), int64(0), "i", "b"},
		{int64(23), int64(16), int64(2557), "e", "f"},
		{int64(16), int64(23), int64(2558), "e", "f"},
		{int64(16), int64(25), int64(2971), "a", "f"},
		{int64(1042), int64(1042), int64(668), "i", "f"},
		{int64(1043), int64(1043), int64(669), "i", "f"},
		{int64(1083), int64(1083), int64(1968), "i", "f"},
		{int64(1114), int64(1114), int64(1961), "i", "f"},
		{int64(1184), int64(1184), int64(1967), "i", "f"},
		{int64(1186), int64(1186), int64(1200), "i", "f"},
		{int64(1266), int64(1266), int64(1969), "i", "f"},
		{int64(1700), int64(1700), int64(1703), "i", "f"},
		{int64(114), int64(3802), int64(0), "a", "i"},
		{int64(3802), int64(114), int64(0), "a", "i"},
		{int64(20), int64(790), int64(3812), "a", "f"},
		{int64(23), int64(790), int64(3811), "a", "f"},
		{int64(790), int64(1700), int64(3823), "a", "f"},
		{int64(1700), int64(790), int64(3824), "a", "f"},
		{int64(5069), int64(28), int64(5071), "e", "f"},
	}
}

func objectAddressClassFacts() [][]any {
	var rows [][]any
	for _, f := range []struct {
		name                                 string
		input, btree, bfamily, hash, hfamily int64
	}{
		{"bool_ops", 16, 10003, 424, 10048, 2222},
		{"bytea_ops", 17, 10006, 428, 10049, 2223},
		{"char_ops", 18, 10007, 429, 10008, 431},
		{"name_ops", 19, 10028, 1994, 10029, 1995},
		{"int2_ops", 21, 1979, 1976, 10019, 1977},
		{"int4_ops", 23, 1978, 1976, 10020, 1977},
		{"int8_ops", 20, 3124, 1976, 10021, 1977},
		{"text_ops", 25, 3126, 1994, 10037, 1995},
		{"varchar_ops", 25, 10044, 1994, 10045, 1995},
		{"bpchar_ops", 1042, 10004, 426, 10005, 427},
		{"oid_ops", 26, 1981, 1989, 10031, 1990},
		{"xid8_ops", 5069, 10053, 5067, 10052, 5032},
		{"xid_ops", 28, 0, 0, 10051, 2225},
		{"cid_ops", 29, 0, 0, 10054, 2226},
		{"float4_ops", 700, 10012, 1970, 10013, 1971},
		{"float8_ops", 701, 3123, 1970, 10014, 1971},
		{"date_ops", 1082, 3122, 434, 10011, 435},
		{"time_ops", 1083, 10038, 1996, 10039, 1997},
		{"timestamp_ops", 1114, 3128, 434, 10046, 2040},
		{"timestamptz_ops", 1184, 3127, 434, 10040, 1999},
		{"timetz_ops", 1266, 10041, 2000, 10042, 2001},
		{"interval_ops", 1186, 10022, 1982, 10023, 1983},
		{"numeric_ops", 1700, 3125, 1988, 10030, 1998},
		{"money_ops", 790, 10047, 2099, 0, 0},
		{"uuid_ops", 2950, 10065, 2968, 10066, 2969},
		{"inet_ops", 869, 10015, 1974, 0, 0},
		{"jsonb_ops", 3802, 10088, 4033, 10089, 4034},
		{"text_pattern_ops", 25, 4217, 2095, 10056, 2229},
		{"varchar_pattern_ops", 25, 4218, 2095, 10057, 2229},
		{"bpchar_pattern_ops", 1042, 4219, 2097, 10058, 2231},
		{"enum_ops", 3500, 10069, 3522, 10070, 3523},
	} {
		def := f.name != "varchar_ops" && !strings.Contains(f.name, "pattern")
		if f.btree != 0 {
			key := int64(0)
			if f.name == "name_ops" {
				key = 2275
			}
			rows = append(rows, []any{f.btree, int64(403), f.name, int64(11), int64(10), f.bfamily, f.input, def, key})
		}
		if f.hash != 0 {
			rows = append(rows, []any{f.hash, int64(405), f.name, int64(11), int64(10), f.hfamily, f.input, def, int64(0)})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0].(int64) < rows[j][0].(int64) })
	return rows
}
