package reference_test

// Independently expanded REL_18_6 pg_operator.dat/pg_cast.dat/pg_type.dat
// facts. See comparison-ledger.md. This is reference evidence, never an engine
// dispatch table. Each character explicitly classifies one ordered type pair.
// The six operator IDs are =, <>, <, <=, >, >=; != uses <>. Zero means the
// explicit released no-match cell, not an unclassified/default outcome.
type comparisonConcreteType struct {
	name, diagnostic, family string
	oid                      int
	element                  int    // -1 for scalars
	low, high                string // contextual public input; special routes are below
}

var comparisonConcrete = []comparisonConcreteType{
	{"bool", "boolean", "BinaryNetwork", 16, -1, "t", "f"},
	{"bytea", "bytea", "BinaryNetwork", 17, -1, "\\x01", "\\x02"},
	{"char", "\"char\"", "Strings", 18, -1, "", "r"},
	{"name", "name", "Strings", 19, -1, "", "b"},
	{"int2", "smallint", "Numeric", 21, -1, "1", "2"},
	{"int4", "integer", "Numeric", 23, -1, "1", "2"},
	{"int8", "bigint", "Numeric", 20, -1, "1", "2"},
	{"float4", "real", "Numeric", 700, -1, "1", "2"},
	{"float8", "double precision", "Numeric", 701, -1, "1", "2"},
	{"numeric", "numeric", "Numeric", 1700, -1, "1", "2"},
	{"money", "money", "Numeric", 790, -1, "1.00", "2.00"},
	{"text", "text", "Strings", 25, -1, "", "b"},
	{"varchar", "character varying", "Strings", 1043, -1, "", "b"},
	{"bpchar", "character", "Strings", 1042, -1, "", "b"},
	{"oid", "oid", "Identifiers", 26, -1, "1", "2"},
	{"regproc", "regproc", "Identifiers", 24, -1, "1", "2"},
	{"regprocedure", "regprocedure", "Identifiers", 2202, -1, "1", "2"},
	{"regoper", "regoper", "Identifiers", 2203, -1, "1", "2"},
	{"regoperator", "regoperator", "Identifiers", 2204, -1, "1", "2"},
	{"regclass", "regclass", "Identifiers", 2205, -1, "1", "2"},
	{"regtype", "regtype", "Identifiers", 2206, -1, "1", "2"},
	{"regrole", "regrole", "Identifiers", 4096, -1, "1", "2"},
	{"regnamespace", "regnamespace", "Identifiers", 4089, -1, "1", "2"},
	{"regcollation", "regcollation", "Identifiers", 4191, -1, "1", "2"},
	{"regconfig", "regconfig", "Identifiers", 3734, -1, "1", "2"},
	{"regdictionary", "regdictionary", "Identifiers", 3769, -1, "1", "2"},
	{"tid", "tid", "Identifiers", 27, -1, "(1,1)", "(1,2)"},
	{"xid", "xid", "Identifiers", 28, -1, "1", "2"},
	{"cid", "cid", "Identifiers", 29, -1, "1", "2"},
	{"xid8", "xid8", "Identifiers", 5069, -1, "1", "2"},
	{"pg_lsn", "pg_lsn", "Identifiers", 3220, -1, "0/1", "80000000/0"},
	{"date", "date", "Temporal", 1082, -1, "2000-01-01", "2000-01-02"},
	{"time", "time without time zone", "Temporal", 1083, -1, "00:00:00", "01:00:00"},
	{"timetz", "time with time zone", "Temporal", 1266, -1, "00:00:00+00", "01:00:00+00"},
	{"timestamp", "timestamp without time zone", "Temporal", 1114, -1, "2000-01-01 00:00:00", "2000-01-02 00:00:00"},
	{"timestamptz", "timestamp with time zone", "Temporal", 1184, -1, "2000-01-01 00:00:00+00", "2000-01-02 00:00:00+00"},
	{"interval", "interval", "Temporal", 1186, -1, "0", "1 hour"},
	{"bit", "bit", "BinaryNetwork", 1560, -1, "0", "1"},
	{"varbit", "bit varying", "BinaryNetwork", 1562, -1, "0", "1"},
	{"uuid", "uuid", "BinaryNetwork", 2950, -1, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"},
	{"inet", "inet", "BinaryNetwork", 869, -1, "0.0.0.0/0", "10.0.0.0/8"},
	{"cidr", "cidr", "BinaryNetwork", 650, -1, "0.0.0.0/0", "10.0.0.0/8"},
	{"json", "json", "NegativeSpace", 114, -1, "1", "2"},
	{"jsonb", "jsonb", "NegativeSpace", 3802, -1, "1", "2"},
	{"jsonpath", "jsonpath", "NegativeSpace", 4072, -1, "$", "$"},
	{"aclitem", "aclitem", "NegativeSpace", 1033, -1, "=r/postgres", "=w/postgres"},
	{"pg_node_tree", "pg_node_tree", "Strings", 194, -1, "", ""},
	{"void", "void", "NegativeSpace", 2278, -1, "", ""},
	{"int4range", "int4range", "Ranges", 3904, -1, "[1,2)", "[2,3)"},
	{"int8range", "int8range", "Ranges", 3926, -1, "[1,2)", "[2,3)"},
	{"numrange", "numrange", "Ranges", 3906, -1, "[1,2)", "[2,3)"},
	{"daterange", "daterange", "Ranges", 3912, -1, "[2000-01-01,2000-01-02)", "[2000-01-02,2000-01-03)"},
	{"tsrange", "tsrange", "Ranges", 3908, -1, "[2000-01-01,2000-01-02)", "[2000-01-02,2000-01-03)"},
	{"tstzrange", "tstzrange", "Ranges", 3910, -1, "[2000-01-01,2000-01-02)", "[2000-01-02,2000-01-03)"},
	{"int4multirange", "int4multirange", "Ranges", 4451, -1, "{[1,2)}", "{[2,3)}"},
	{"int8multirange", "int8multirange", "Ranges", 4536, -1, "{[1,2)}", "{[2,3)}"},
	{"nummultirange", "nummultirange", "Ranges", 4532, -1, "{[1,2)}", "{[2,3)}"},
	{"bool[]", "boolean[]", "Arrays", 1000, 0, "", ""},
	{"bytea[]", "bytea[]", "Arrays", 1001, 1, "", ""},
	{"name[]", "name[]", "Arrays", 1003, 3, "", ""},
	{"int2[]", "smallint[]", "Arrays", 1005, 4, "", ""},
	{"int4[]", "integer[]", "Arrays", 1007, 5, "", ""},
	{"int8[]", "bigint[]", "Arrays", 1016, 6, "", ""},
	{"text[]", "text[]", "Arrays", 1009, 11, "", ""},
	{"oid[]", "oid[]", "Arrays", 1028, 14, "", ""},
	{"json[]", "json[]", "Arrays", 199, 42, "", ""},
	{"jsonb[]", "jsonb[]", "Arrays", 3807, 43, "", ""},
	{"jsonpath[]", "jsonpath[]", "Arrays", 4073, 44, "", ""},
	{"bpchar[]", "character[]", "Arrays", 1014, 13, "", ""},
	{"varchar[]", "character varying[]", "Arrays", 1015, 12, "", ""},
	{"float4[]", "real[]", "Arrays", 1021, 7, "", ""},
	{"float8[]", "double precision[]", "Arrays", 1022, 8, "", ""},
	{"date[]", "date[]", "Arrays", 1182, 31, "", ""},
	{"time[]", "time without time zone[]", "Arrays", 1183, 32, "", ""},
	{"timetz[]", "time with time zone[]", "Arrays", 1270, 33, "", ""},
	{"timestamp[]", "timestamp without time zone[]", "Arrays", 1115, 34, "", ""},
	{"timestamptz[]", "timestamp with time zone[]", "Arrays", 1185, 35, "", ""},
	{"interval[]", "interval[]", "Arrays", 1187, 36, "", ""},
	{"numeric[]", "numeric[]", "Arrays", 1231, 9, "", ""},
	{"uuid[]", "uuid[]", "Arrays", 2951, 39, "", ""},
	{"regclass[]", "regclass[]", "Arrays", 2210, 19, "", ""},
	{"regtype[]", "regtype[]", "Arrays", 2211, 20, "", ""},
	{"regproc[]", "regproc[]", "Arrays", 1008, 15, "", ""},
	{"regprocedure[]", "regprocedure[]", "Arrays", 2207, 16, "", ""},
	{"regrole[]", "regrole[]", "Arrays", 4097, 21, "", ""},
	{"regcollation[]", "regcollation[]", "Arrays", 4192, 23, "", ""},
	{"regnamespace[]", "regnamespace[]", "Arrays", 4090, 22, "", ""},
	{"aclitem[]", "aclitem[]", "Arrays", 1034, 45, "", ""},
}

type comparisonReleasedSignature struct {
	left, right string
	operators   [6]int
	routines    [6]string
}

var comparisonReleasedSignatures = []comparisonReleasedSignature{
	{"bool", "bool", [6]int{91, 85, 58, 1694, 59, 1695}, [6]string{"booleq", "boolne", "boollt", "boolle", "boolgt", "boolge"}},
	{"", "", [6]int{0, 0, 0, 0, 0, 0}, [6]string{"", "", "", "", "", ""}},
	{"bytea", "bytea", [6]int{1955, 1956, 1957, 1958, 1959, 1960}, [6]string{"byteaeq", "byteane", "bytealt", "byteale", "byteagt", "byteage"}},
	{"char", "char", [6]int{92, 630, 631, 632, 633, 634}, [6]string{"chareq", "charne", "charlt", "charle", "chargt", "charge"}},
	{"text", "name", [6]int{260, 265, 261, 262, 264, 263}, [6]string{"texteqname", "textnename", "textltname", "textlename", "textgtname", "textgename"}},
	{"text", "text", [6]int{98, 531, 664, 665, 666, 667}, [6]string{"texteq", "textne", "text_lt", "text_le", "text_gt", "text_ge"}},
	{"name", "text", [6]int{254, 259, 255, 256, 258, 257}, [6]string{"nameeqtext", "namenetext", "namelttext", "nameletext", "namegttext", "namegetext"}},
	{"name", "name", [6]int{93, 643, 660, 661, 662, 663}, [6]string{"nameeq", "namene", "namelt", "namele", "namegt", "namege"}},
	{"int2", "int2", [6]int{94, 519, 95, 522, 520, 524}, [6]string{"int2eq", "int2ne", "int2lt", "int2le", "int2gt", "int2ge"}},
	{"int2", "int4", [6]int{532, 538, 534, 540, 536, 542}, [6]string{"int24eq", "int24ne", "int24lt", "int24le", "int24gt", "int24ge"}},
	{"int2", "int8", [6]int{1862, 1863, 1864, 1866, 1865, 1867}, [6]string{"int28eq", "int28ne", "int28lt", "int28le", "int28gt", "int28ge"}},
	{"float8", "float4", [6]int{1130, 1131, 1132, 1134, 1133, 1135}, [6]string{"float84eq", "float84ne", "float84lt", "float84le", "float84gt", "float84ge"}},
	{"float8", "float8", [6]int{670, 671, 672, 673, 674, 675}, [6]string{"float8eq", "float8ne", "float8lt", "float8le", "float8gt", "float8ge"}},
	{"numeric", "numeric", [6]int{1752, 1753, 1754, 1755, 1756, 1757}, [6]string{"numeric_eq", "numeric_ne", "numeric_lt", "numeric_le", "numeric_gt", "numeric_ge"}},
	{"oid", "oid", [6]int{607, 608, 609, 611, 610, 612}, [6]string{"oideq", "oidne", "oidlt", "oidle", "oidgt", "oidge"}},
	{"int4", "int2", [6]int{533, 539, 535, 541, 537, 543}, [6]string{"int42eq", "int42ne", "int42lt", "int42le", "int42gt", "int42ge"}},
	{"int4", "int4", [6]int{96, 518, 97, 523, 521, 525}, [6]string{"int4eq", "int4ne", "int4lt", "int4le", "int4gt", "int4ge"}},
	{"int4", "int8", [6]int{15, 36, 37, 80, 76, 82}, [6]string{"int48eq", "int48ne", "int48lt", "int48le", "int48gt", "int48ge"}},
	{"int8", "int2", [6]int{1868, 1869, 1870, 1872, 1871, 1873}, [6]string{"int82eq", "int82ne", "int82lt", "int82le", "int82gt", "int82ge"}},
	{"int8", "int4", [6]int{416, 417, 418, 420, 419, 430}, [6]string{"int84eq", "int84ne", "int84lt", "int84le", "int84gt", "int84ge"}},
	{"int8", "int8", [6]int{410, 411, 412, 414, 413, 415}, [6]string{"int8eq", "int8ne", "int8lt", "int8le", "int8gt", "int8ge"}},
	{"float4", "float8", [6]int{1120, 1121, 1122, 1124, 1123, 1125}, [6]string{"float48eq", "float48ne", "float48lt", "float48le", "float48gt", "float48ge"}},
	{"float4", "float4", [6]int{620, 621, 622, 624, 623, 625}, [6]string{"float4eq", "float4ne", "float4lt", "float4le", "float4gt", "float4ge"}},
	{"money", "money", [6]int{900, 901, 902, 904, 903, 905}, [6]string{"cash_eq", "cash_ne", "cash_lt", "cash_le", "cash_gt", "cash_ge"}},
	{"bpchar", "bpchar", [6]int{1054, 1057, 1058, 1059, 1060, 1061}, [6]string{"bpchareq", "bpcharne", "bpcharlt", "bpcharle", "bpchargt", "bpcharge"}},
	{"tid", "tid", [6]int{387, 402, 2799, 2801, 2800, 2802}, [6]string{"tideq", "tidne", "tidlt", "tidle", "tidgt", "tidge"}},
	{"xid", "int4", [6]int{353, 3316, 0, 0, 0, 0}, [6]string{"xideqint4", "xidneqint4", "", "", "", ""}},
	{"xid", "xid", [6]int{352, 3315, 0, 0, 0, 0}, [6]string{"xideq", "xidneq", "", "", "", ""}},
	{"cid", "cid", [6]int{385, 0, 0, 0, 0, 0}, [6]string{"cideq", "", "", "", "", ""}},
	{"xid8", "xid8", [6]int{5068, 5072, 5073, 5075, 5074, 5076}, [6]string{"xid8eq", "xid8ne", "xid8lt", "xid8le", "xid8gt", "xid8ge"}},
	{"date", "date", [6]int{1093, 1094, 1095, 1096, 1097, 1098}, [6]string{"date_eq", "date_ne", "date_lt", "date_le", "date_gt", "date_ge"}},
	{"date", "timestamp", [6]int{2347, 2350, 2345, 2346, 2349, 2348}, [6]string{"date_eq_timestamp", "date_ne_timestamp", "date_lt_timestamp", "date_le_timestamp", "date_gt_timestamp", "date_ge_timestamp"}},
	{"date", "timestamptz", [6]int{2360, 2363, 2358, 2359, 2362, 2361}, [6]string{"date_eq_timestamptz", "date_ne_timestamptz", "date_lt_timestamptz", "date_le_timestamptz", "date_gt_timestamptz", "date_ge_timestamptz"}},
	{"time", "time", [6]int{1108, 1109, 1110, 1111, 1112, 1113}, [6]string{"time_eq", "time_ne", "time_lt", "time_le", "time_gt", "time_ge"}},
	{"timetz", "timetz", [6]int{1550, 1551, 1552, 1553, 1554, 1555}, [6]string{"timetz_eq", "timetz_ne", "timetz_lt", "timetz_le", "timetz_gt", "timetz_ge"}},
	{"interval", "interval", [6]int{1330, 1331, 1332, 1333, 1334, 1335}, [6]string{"interval_eq", "interval_ne", "interval_lt", "interval_le", "interval_gt", "interval_ge"}},
	{"timestamp", "date", [6]int{2373, 2376, 2371, 2372, 2375, 2374}, [6]string{"timestamp_eq_date", "timestamp_ne_date", "timestamp_lt_date", "timestamp_le_date", "timestamp_gt_date", "timestamp_ge_date"}},
	{"timestamp", "timestamp", [6]int{2060, 2061, 2062, 2063, 2064, 2065}, [6]string{"timestamp_eq", "timestamp_ne", "timestamp_lt", "timestamp_le", "timestamp_gt", "timestamp_ge"}},
	{"timestamp", "timestamptz", [6]int{2536, 2539, 2534, 2535, 2538, 2537}, [6]string{"timestamp_eq_timestamptz", "timestamp_ne_timestamptz", "timestamp_lt_timestamptz", "timestamp_le_timestamptz", "timestamp_gt_timestamptz", "timestamp_ge_timestamptz"}},
	{"timestamptz", "date", [6]int{2386, 2389, 2384, 2385, 2388, 2387}, [6]string{"timestamptz_eq_date", "timestamptz_ne_date", "timestamptz_lt_date", "timestamptz_le_date", "timestamptz_gt_date", "timestamptz_ge_date"}},
	{"timestamptz", "timestamp", [6]int{2542, 2545, 2540, 2541, 2544, 2543}, [6]string{"timestamptz_eq_timestamp", "timestamptz_ne_timestamp", "timestamptz_lt_timestamp", "timestamptz_le_timestamp", "timestamptz_gt_timestamp", "timestamptz_ge_timestamp"}},
	{"timestamptz", "timestamptz", [6]int{1320, 1321, 1322, 1323, 1324, 1325}, [6]string{"timestamptz_eq", "timestamptz_ne", "timestamptz_lt", "timestamptz_le", "timestamptz_gt", "timestamptz_ge"}},
	{"bit", "bit", [6]int{1784, 1785, 1786, 1788, 1787, 1789}, [6]string{"biteq", "bitne", "bitlt", "bitle", "bitgt", "bitge"}},
	{"varbit", "varbit", [6]int{1804, 1805, 1806, 1808, 1807, 1809}, [6]string{"varbiteq", "varbitne", "varbitlt", "varbitle", "varbitgt", "varbitge"}},
	{"uuid", "uuid", [6]int{2972, 2973, 2974, 2976, 2975, 2977}, [6]string{"uuid_eq", "uuid_ne", "uuid_lt", "uuid_le", "uuid_gt", "uuid_ge"}},
	{"inet", "inet", [6]int{1201, 1202, 1203, 1204, 1205, 1206}, [6]string{"network_eq", "network_ne", "network_lt", "network_le", "network_gt", "network_ge"}},
	{"jsonb", "jsonb", [6]int{3240, 3241, 3242, 3244, 3243, 3245}, [6]string{"jsonb_eq", "jsonb_ne", "jsonb_lt", "jsonb_le", "jsonb_gt", "jsonb_ge"}},
	{"aclitem", "aclitem", [6]int{974, 0, 0, 0, 0, 0}, [6]string{"aclitemeq", "", "", "", "", ""}},
	{"anyrange", "anyrange", [6]int{3882, 3883, 3884, 3885, 3887, 3886}, [6]string{"range_eq", "range_ne", "range_lt", "range_le", "range_gt", "range_ge"}},
	{"anymultirange", "anymultirange", [6]int{2860, 2861, 2862, 2863, 2865, 2864}, [6]string{"multirange_eq", "multirange_ne", "multirange_lt", "multirange_le", "multirange_gt", "multirange_ge"}},
	{"anyarray", "anyarray", [6]int{1070, 1071, 1072, 1074, 1073, 1075}, [6]string{"array_eq", "array_ne", "array_lt", "array_le", "array_gt", "array_ge"}},
	{"pg_lsn", "pg_lsn", [6]int{3222, 3223, 3224, 3226, 3225, 3227}, [6]string{"pg_lsn_eq", "pg_lsn_ne", "pg_lsn_lt", "pg_lsn_le", "pg_lsn_gt", "pg_lsn_ge"}},
}

const comparisonLedgerAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var comparisonReleasedCells = []string{
	"ABBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // bool
	"BCBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // bytea
	"BBDEBBBBBBBFFFBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBFBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // char
	"BBGHBBBBBBBGGGBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBGBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // name
	"BBBBIJKLMNBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // int2
	"BBBBPQRLMNBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // int4
	"BBBBSTULMNBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // int8
	"BBBBVVVWVVBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // float4
	"BBBBMMMLMMBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // float8
	"BBBBNNNLMNBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // numeric
	"BBBBBBBBBBXBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // money
	"BBFEBBBBBBBFFFBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBFBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // text
	"BBFEBBBBBBBFFYBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBFBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // varchar
	"BBFEBBBBBBBFYYBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBFBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // bpchar
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // oid
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regproc
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regprocedure
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regoper
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regoperator
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regclass
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regtype
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regrole
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regnamespace
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regcollation
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regconfig
	"BBBBOOOBBBBBBBOOOOOOOOOOOOBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // regdictionary
	"BBBBBBBBBBBBBBBBBBBBBBBBBBZBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // tid
	"BBBBaaBBBBBBBBBBBBBBBBBBBBBbBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // xid
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBcBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // cid
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBdBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // xid8
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBzBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // pg_lsn
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBeBBfgBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // date
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBhiBBjBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // time
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBiiBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // timetz
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBkBBlmBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // timestamp
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBnBBopBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // timestamptz
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBjBBBjBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // interval
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBqrBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // bit
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBrrBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // varbit
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBsBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // uuid
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBttBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // inet
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBttBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // cidr
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // json
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBuBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // jsonb
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // jsonpath
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBvBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // aclitem
	"BBFEBBBBBBBFFFBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBFBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // pg_node_tree
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // void
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBwBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // int4range
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBwBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // int8range
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBwBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // numrange
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBwBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // daterange
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBwBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // tsrange
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBwBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // tstzrange
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBxBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // int4multirange
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBxBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // int8multirange
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBxBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // nummultirange
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // bool[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // bytea[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBBBBBBBBBB", // name[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBBBBBBBBB", // int2[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBBBBBBBB", // int4[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBBBBBBB", // int8[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBBBBBB", // text[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBBBBB", // oid[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBBBB", // json[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBBB", // jsonb[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBBB", // jsonpath[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBBB", // bpchar[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBBB", // varchar[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBBB", // float4[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBBB", // float8[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBBB", // date[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBBB", // time[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBBB", // timetz[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBBB", // timestamp[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBBB", // timestamptz[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBBB", // interval[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBBB", // numeric[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBBB", // uuid[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBBB", // regclass[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBBB", // regtype[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBBB", // regproc[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBBB", // regprocedure[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBBB", // regrole[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByBB", // regcollation[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBByB", // regnamespace[]
	"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBy", // aclitem[]
}
