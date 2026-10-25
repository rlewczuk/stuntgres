package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Literal independent release data: REL_18_6 pg_type.dat, pg_type.h,
// pg_proc.dat and Catalog.pm GenerateArrayTypes. Eight-byte Datum, NAMEDATALEN64.
// These are catalog expectations, not a native-provider-derived golden.
var pgTypeBuiltinFacts = [][]any{
	{int64(16), "bool", int64(11), int64(10), int64(1), true, "b", "B", true, true, ",", int64(0), int64(0), int64(0), int64(1000), int64(1242), int64(1243), int64(2436), int64(2437), int64(0), int64(0), int64(0), "c", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(17), "bytea", int64(11), int64(10), int64(-1), false, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(1001), int64(1244), int64(31), int64(2412), int64(2413), int64(0), int64(0), int64(0), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(18), "char", int64(11), int64(10), int64(1), true, "b", "Z", false, true, ",", int64(0), int64(0), int64(0), int64(1002), int64(1245), int64(33), int64(2434), int64(2435), int64(0), int64(0), int64(0), "c", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(19), "name", int64(11), int64(10), int64(64), false, "b", "S", false, true, ",", int64(0), int64(6180), int64(18), int64(1003), int64(34), int64(35), int64(2422), int64(2423), int64(0), int64(0), int64(0), "c", "p", false, int64(0), int64(-1), int64(0), int64(950), nil, nil, nil},
	{int64(20), "int8", int64(11), int64(10), int64(8), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(1016), int64(460), int64(461), int64(2408), int64(2409), int64(0), int64(0), int64(0), "d", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(21), "int2", int64(11), int64(10), int64(2), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(1005), int64(38), int64(39), int64(2404), int64(2405), int64(0), int64(0), int64(0), "s", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(22), "int2vector", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(21), int64(1006), int64(40), int64(41), int64(2410), int64(2411), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(23), "int4", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(1007), int64(42), int64(43), int64(2406), int64(2407), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(24), "regproc", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(1008), int64(44), int64(45), int64(2444), int64(2445), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(25), "text", int64(11), int64(10), int64(-1), false, "b", "S", true, true, ",", int64(0), int64(0), int64(0), int64(1009), int64(46), int64(47), int64(2414), int64(2415), int64(0), int64(0), int64(0), "i", "x", false, int64(0), int64(-1), int64(0), int64(100), nil, nil, nil},
	{int64(26), "oid", int64(11), int64(10), int64(4), true, "b", "N", true, true, ",", int64(0), int64(0), int64(0), int64(1028), int64(1798), int64(1799), int64(2418), int64(2419), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(27), "tid", int64(11), int64(10), int64(6), false, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(1010), int64(48), int64(49), int64(2438), int64(2439), int64(0), int64(0), int64(0), "s", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(28), "xid", int64(11), int64(10), int64(4), true, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(1011), int64(50), int64(51), int64(2440), int64(2441), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(29), "cid", int64(11), int64(10), int64(4), true, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(1012), int64(52), int64(53), int64(2442), int64(2443), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(30), "oidvector", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(26), int64(1013), int64(54), int64(55), int64(2420), int64(2421), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(114), "json", int64(11), int64(10), int64(-1), false, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(199), int64(321), int64(322), int64(323), int64(324), int64(0), int64(0), int64(0), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(194), "pg_node_tree", int64(11), int64(10), int64(-1), false, "b", "Z", false, true, ",", int64(0), int64(0), int64(0), int64(0), int64(195), int64(196), int64(197), int64(198), int64(0), int64(0), int64(0), "i", "x", false, int64(0), int64(-1), int64(0), int64(100), nil, nil, nil},
	{int64(199), "_json", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(114), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(271), "_xid8", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(5069), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(650), "cidr", int64(11), int64(10), int64(-1), false, "b", "I", false, true, ",", int64(0), int64(0), int64(0), int64(651), int64(1267), int64(1427), int64(2498), int64(2499), int64(0), int64(0), int64(0), "i", "m", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(651), "_cidr", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(650), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(700), "float4", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(1021), int64(200), int64(201), int64(2424), int64(2425), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(701), "float8", int64(11), int64(10), int64(8), true, "b", "N", true, true, ",", int64(0), int64(0), int64(0), int64(1022), int64(214), int64(215), int64(2426), int64(2427), int64(0), int64(0), int64(0), "d", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(705), "unknown", int64(11), int64(10), int64(-2), false, "p", "X", false, true, ",", int64(0), int64(0), int64(0), int64(0), int64(109), int64(110), int64(2416), int64(2417), int64(0), int64(0), int64(0), "c", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(790), "money", int64(11), int64(10), int64(8), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(791), int64(886), int64(887), int64(2492), int64(2493), int64(0), int64(0), int64(0), "d", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(791), "_money", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(790), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(869), "inet", int64(11), int64(10), int64(-1), false, "b", "I", true, true, ",", int64(0), int64(0), int64(0), int64(1041), int64(910), int64(911), int64(2496), int64(2497), int64(0), int64(0), int64(0), "i", "m", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1000), "_bool", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(16), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1001), "_bytea", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(17), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1002), "_char", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(18), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1003), "_name", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(19), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(950), nil, nil, nil},
	{int64(1005), "_int2", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(21), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1007), "_int4", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(23), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1008), "_regproc", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(24), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1009), "_text", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(25), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(100), nil, nil, nil},
	{int64(1010), "_tid", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(27), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1011), "_xid", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(28), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1012), "_cid", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(29), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1014), "_bpchar", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1042), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(2913), int64(2914), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(100), nil, nil, nil},
	{int64(1015), "_varchar", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1043), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(2915), int64(2916), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(100), nil, nil, nil},
	{int64(1016), "_int8", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(20), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1021), "_float4", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(700), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1022), "_float8", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(701), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1028), "_oid", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(26), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1033), "aclitem", int64(11), int64(10), int64(16), false, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(1034), int64(1031), int64(1032), int64(0), int64(0), int64(0), int64(0), int64(0), "d", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1034), "_aclitem", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1033), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1041), "_inet", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(869), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1042), "bpchar", int64(11), int64(10), int64(-1), false, "b", "S", false, true, ",", int64(0), int64(0), int64(0), int64(1014), int64(1044), int64(1045), int64(2430), int64(2431), int64(2913), int64(2914), int64(0), "i", "x", false, int64(0), int64(-1), int64(0), int64(100), nil, nil, nil},
	{int64(1043), "varchar", int64(11), int64(10), int64(-1), false, "b", "S", false, true, ",", int64(0), int64(0), int64(0), int64(1015), int64(1046), int64(1047), int64(2432), int64(2433), int64(2915), int64(2916), int64(0), "i", "x", false, int64(0), int64(-1), int64(0), int64(100), nil, nil, nil},
	{int64(1082), "date", int64(11), int64(10), int64(4), true, "b", "D", false, true, ",", int64(0), int64(0), int64(0), int64(1182), int64(1084), int64(1085), int64(2468), int64(2469), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1083), "time", int64(11), int64(10), int64(8), true, "b", "D", false, true, ",", int64(0), int64(0), int64(0), int64(1183), int64(1143), int64(1144), int64(2470), int64(2471), int64(2909), int64(2910), int64(0), "d", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1114), "timestamp", int64(11), int64(10), int64(8), true, "b", "D", false, true, ",", int64(0), int64(0), int64(0), int64(1115), int64(1312), int64(1313), int64(2474), int64(2475), int64(2905), int64(2906), int64(0), "d", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1115), "_timestamp", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1114), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(2905), int64(2906), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1182), "_date", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1082), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1183), "_time", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1083), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(2909), int64(2910), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1184), "timestamptz", int64(11), int64(10), int64(8), true, "b", "D", true, true, ",", int64(0), int64(0), int64(0), int64(1185), int64(1150), int64(1151), int64(2476), int64(2477), int64(2907), int64(2908), int64(0), "d", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1185), "_timestamptz", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1184), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(2907), int64(2908), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1186), "interval", int64(11), int64(10), int64(16), false, "b", "T", true, true, ",", int64(0), int64(0), int64(0), int64(1187), int64(1160), int64(1161), int64(2478), int64(2479), int64(2903), int64(2904), int64(0), "d", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1187), "_interval", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1186), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(2903), int64(2904), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1231), "_numeric", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1700), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(2917), int64(2918), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1266), "timetz", int64(11), int64(10), int64(12), false, "b", "D", false, true, ",", int64(0), int64(0), int64(0), int64(1270), int64(1350), int64(1351), int64(2472), int64(2473), int64(2911), int64(2912), int64(0), "d", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1270), "_timetz", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1266), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(2911), int64(2912), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1560), "bit", int64(11), int64(10), int64(-1), false, "b", "V", false, true, ",", int64(0), int64(0), int64(0), int64(1561), int64(1564), int64(1565), int64(2456), int64(2457), int64(2919), int64(2920), int64(0), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1561), "_bit", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1560), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(2919), int64(2920), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1562), "varbit", int64(11), int64(10), int64(-1), false, "b", "V", true, true, ",", int64(0), int64(0), int64(0), int64(1563), int64(1579), int64(1580), int64(2458), int64(2459), int64(2902), int64(2921), int64(0), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1563), "_varbit", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(1562), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(2902), int64(2921), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(1700), "numeric", int64(11), int64(10), int64(-1), false, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(1231), int64(1701), int64(1702), int64(2460), int64(2461), int64(2917), int64(2918), int64(0), "i", "m", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2202), "regprocedure", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(2207), int64(2212), int64(2213), int64(2446), int64(2447), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2203), "regoper", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(2208), int64(2214), int64(2215), int64(2448), int64(2449), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2204), "regoperator", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(2209), int64(2216), int64(2217), int64(2450), int64(2451), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2205), "regclass", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(2210), int64(2218), int64(2219), int64(2452), int64(2453), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2206), "regtype", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(2211), int64(2220), int64(2221), int64(2454), int64(2455), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2207), "_regprocedure", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(2202), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2208), "_regoper", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(2203), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2209), "_regoperator", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(2204), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2210), "_regclass", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(2205), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2211), "_regtype", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(2206), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2249), "record", int64(11), int64(10), int64(-1), false, "p", "P", false, true, ",", int64(0), int64(0), int64(0), int64(2287), int64(2290), int64(2291), int64(2402), int64(2403), int64(0), int64(0), int64(0), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2277), "anyarray", int64(11), int64(10), int64(-1), false, "p", "P", false, true, ",", int64(0), int64(0), int64(0), int64(0), int64(2296), int64(2297), int64(2502), int64(2503), int64(0), int64(0), int64(0), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2278), "void", int64(11), int64(10), int64(4), true, "p", "P", false, true, ",", int64(0), int64(0), int64(0), int64(0), int64(2298), int64(2299), int64(3120), int64(3121), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2287), "_record", int64(11), int64(10), int64(-1), false, "p", "P", false, true, ",", int64(0), int64(6179), int64(2249), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2950), "uuid", int64(11), int64(10), int64(16), false, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(2951), int64(2952), int64(2953), int64(2961), int64(2962), int64(0), int64(0), int64(0), "c", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(2951), "_uuid", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(2950), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3614), "tsvector", int64(11), int64(10), int64(-1), false, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(3643), int64(3610), int64(3611), int64(3639), int64(3638), int64(0), int64(0), int64(3688), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3615), "tsquery", int64(11), int64(10), int64(-1), false, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(3645), int64(3612), int64(3613), int64(3641), int64(3640), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3643), "_tsvector", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3614), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3645), "_tsquery", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3615), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3734), "regconfig", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(3735), int64(3736), int64(3737), int64(3738), int64(3739), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3735), "_regconfig", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3734), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3769), "regdictionary", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(3770), int64(3771), int64(3772), int64(3773), int64(3774), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3770), "_regdictionary", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3769), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3802), "jsonb", int64(11), int64(10), int64(-1), false, "b", "U", false, true, ",", int64(0), int64(6098), int64(0), int64(3807), int64(3806), int64(3804), int64(3805), int64(3803), int64(0), int64(0), int64(0), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3807), "_jsonb", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3802), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3904), "int4range", int64(11), int64(10), int64(-1), false, "r", "R", false, true, ",", int64(0), int64(0), int64(0), int64(3905), int64(3834), int64(3835), int64(3836), int64(3837), int64(0), int64(0), int64(3916), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3905), "_int4range", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3904), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3906), "numrange", int64(11), int64(10), int64(-1), false, "r", "R", false, true, ",", int64(0), int64(0), int64(0), int64(3907), int64(3834), int64(3835), int64(3836), int64(3837), int64(0), int64(0), int64(3916), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3907), "_numrange", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3906), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3908), "tsrange", int64(11), int64(10), int64(-1), false, "r", "R", false, true, ",", int64(0), int64(0), int64(0), int64(3909), int64(3834), int64(3835), int64(3836), int64(3837), int64(0), int64(0), int64(3916), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3909), "_tsrange", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3908), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3910), "tstzrange", int64(11), int64(10), int64(-1), false, "r", "R", false, true, ",", int64(0), int64(0), int64(0), int64(3911), int64(3834), int64(3835), int64(3836), int64(3837), int64(0), int64(0), int64(3916), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3911), "_tstzrange", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3910), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3912), "daterange", int64(11), int64(10), int64(-1), false, "r", "R", false, true, ",", int64(0), int64(0), int64(0), int64(3913), int64(3834), int64(3835), int64(3836), int64(3837), int64(0), int64(0), int64(3916), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3913), "_daterange", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3912), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3926), "int8range", int64(11), int64(10), int64(-1), false, "r", "R", false, true, ",", int64(0), int64(0), int64(0), int64(3927), int64(3834), int64(3835), int64(3836), int64(3837), int64(0), int64(0), int64(3916), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(3927), "_int8range", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(3926), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4072), "jsonpath", int64(11), int64(10), int64(-1), false, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(4073), int64(4001), int64(4003), int64(4002), int64(4004), int64(0), int64(0), int64(0), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4073), "_jsonpath", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(4072), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4089), "regnamespace", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(4090), int64(4084), int64(4085), int64(4087), int64(4088), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4090), "_regnamespace", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(4089), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4096), "regrole", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(4097), int64(4098), int64(4092), int64(4094), int64(4095), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4097), "_regrole", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(4096), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4191), "regcollation", int64(11), int64(10), int64(4), true, "b", "N", false, true, ",", int64(0), int64(0), int64(0), int64(4192), int64(4193), int64(4194), int64(4196), int64(4197), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4192), "_regcollation", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(4191), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4451), "int4multirange", int64(11), int64(10), int64(-1), false, "m", "R", false, true, ",", int64(0), int64(0), int64(0), int64(6150), int64(4231), int64(4232), int64(4233), int64(4234), int64(0), int64(0), int64(4242), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4532), "nummultirange", int64(11), int64(10), int64(-1), false, "m", "R", false, true, ",", int64(0), int64(0), int64(0), int64(6151), int64(4231), int64(4232), int64(4233), int64(4234), int64(0), int64(0), int64(4242), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(4536), "int8multirange", int64(11), int64(10), int64(-1), false, "m", "R", false, true, ",", int64(0), int64(0), int64(0), int64(6157), int64(4231), int64(4232), int64(4233), int64(4234), int64(0), int64(0), int64(4242), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(5069), "xid8", int64(11), int64(10), int64(8), true, "b", "U", false, true, ",", int64(0), int64(0), int64(0), int64(271), int64(5070), int64(5081), int64(5082), int64(5083), int64(0), int64(0), int64(0), "d", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(6150), "_int4multirange", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(4451), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(6151), "_nummultirange", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(4532), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "i", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
	{int64(6157), "_int8multirange", int64(11), int64(10), int64(-1), false, "b", "A", false, true, ",", int64(0), int64(6179), int64(4536), int64(0), int64(750), int64(751), int64(2400), int64(2401), int64(0), int64(0), int64(3816), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil},
}

var pgTypeRoutineNames = map[int64]string{
	31:   "byteaout",
	33:   "charout",
	34:   "namein",
	35:   "nameout",
	38:   "int2in",
	39:   "int2out",
	40: "int2vectorin",
	41: "int2vectorout",
	54: "oidvectorin",
	55: "oidvectorout",
	2410: "int2vectorrecv",
	2411: "int2vectorsend",
	2420: "oidvectorrecv",
	2421: "oidvectorsend",
	42:   "int4in",
	43:   "int4out",
	44:   "regprocin",
	45:   "regprocout",
	46:   "textin",
	47:   "textout",
	48:   "tidin",
	49:   "tidout",
	50:   "xidin",
	51:   "xidout",
	52:   "cidin",
	53:   "cidout",
	109:  "unknownin",
	110:  "unknownout",
	195:  "pg_node_tree_in",
	196:  "pg_node_tree_out",
	197:  "pg_node_tree_recv",
	198:  "pg_node_tree_send",
	200:  "float4in",
	201:  "float4out",
	214:  "float8in",
	215:  "float8out",
	321:  "json_in",
	322:  "json_out",
	323:  "json_recv",
	324:  "json_send",
	460:  "int8in",
	461:  "int8out",
	750:  "array_in",
	751:  "array_out",
	886:  "cash_in",
	887:  "cash_out",
	910:  "inet_in",
	911:  "inet_out",
	1031: "aclitemin",
	1032: "aclitemout",
	1044: "bpcharin",
	1045: "bpcharout",
	1046: "varcharin",
	1047: "varcharout",
	1084: "date_in",
	1085: "date_out",
	1143: "time_in",
	1144: "time_out",
	1150: "timestamptz_in",
	1151: "timestamptz_out",
	1160: "interval_in",
	1161: "interval_out",
	1242: "boolin",
	1243: "boolout",
	1244: "byteain",
	1245: "charin",
	1267: "cidr_in",
	1312: "timestamp_in",
	1313: "timestamp_out",
	1350: "timetz_in",
	1351: "timetz_out",
	1427: "cidr_out",
	1564: "bit_in",
	1565: "bit_out",
	1579: "varbit_in",
	1580: "varbit_out",
	1701: "numeric_in",
	1702: "numeric_out",
	1798: "oidin",
	1799: "oidout",
	2212: "regprocedurein",
	2213: "regprocedureout",
	2214: "regoperin",
	2215: "regoperout",
	2216: "regoperatorin",
	2217: "regoperatorout",
	2218: "regclassin",
	2219: "regclassout",
	2220: "regtypein",
	2221: "regtypeout",
	2290: "record_in",
	2291: "record_out",
	2296: "anyarray_in",
	2297: "anyarray_out",
	2298: "void_in",
	2299: "void_out",
	2400: "array_recv",
	2401: "array_send",
	2402: "record_recv",
	2403: "record_send",
	2404: "int2recv",
	2405: "int2send",
	2406: "int4recv",
	2407: "int4send",
	2408: "int8recv",
	2409: "int8send",
	2412: "bytearecv",
	2413: "byteasend",
	2414: "textrecv",
	2415: "textsend",
	2416: "unknownrecv",
	2417: "unknownsend",
	2418: "oidrecv",
	2419: "oidsend",
	2422: "namerecv",
	2423: "namesend",
	2424: "float4recv",
	2425: "float4send",
	2426: "float8recv",
	2427: "float8send",
	2430: "bpcharrecv",
	2431: "bpcharsend",
	2432: "varcharrecv",
	2433: "varcharsend",
	2434: "charrecv",
	2435: "charsend",
	2436: "boolrecv",
	2437: "boolsend",
	2438: "tidrecv",
	2439: "tidsend",
	2440: "xidrecv",
	2441: "xidsend",
	2442: "cidrecv",
	2443: "cidsend",
	2444: "regprocrecv",
	2445: "regprocsend",
	2446: "regprocedurerecv",
	2447: "regproceduresend",
	2448: "regoperrecv",
	2449: "regopersend",
	2450: "regoperatorrecv",
	2451: "regoperatorsend",
	2452: "regclassrecv",
	2453: "regclasssend",
	2454: "regtyperecv",
	2455: "regtypesend",
	2456: "bit_recv",
	2457: "bit_send",
	2458: "varbit_recv",
	2459: "varbit_send",
	2460: "numeric_recv",
	2461: "numeric_send",
	2468: "date_recv",
	2469: "date_send",
	2470: "time_recv",
	2471: "time_send",
	2472: "timetz_recv",
	2473: "timetz_send",
	2474: "timestamp_recv",
	2475: "timestamp_send",
	2476: "timestamptz_recv",
	2477: "timestamptz_send",
	2478: "interval_recv",
	2479: "interval_send",
	2492: "cash_recv",
	2493: "cash_send",
	2496: "inet_recv",
	2497: "inet_send",
	2498: "cidr_recv",
	2499: "cidr_send",
	2502: "anyarray_recv",
	2503: "anyarray_send",
	2902: "varbittypmodin",
	2903: "intervaltypmodin",
	2904: "intervaltypmodout",
	2905: "timestamptypmodin",
	2906: "timestamptypmodout",
	2907: "timestamptztypmodin",
	2908: "timestamptztypmodout",
	2909: "timetypmodin",
	2910: "timetypmodout",
	2911: "timetztypmodin",
	2912: "timetztypmodout",
	2913: "bpchartypmodin",
	2914: "bpchartypmodout",
	2915: "varchartypmodin",
	2916: "varchartypmodout",
	2917: "numerictypmodin",
	2918: "numerictypmodout",
	2919: "bittypmodin",
	2920: "bittypmodout",
	2921: "varbittypmodout",
	2952: "uuid_in",
	2953: "uuid_out",
	2961: "uuid_recv",
	2962: "uuid_send",
	3120: "void_recv",
	3121: "void_send",
	3610: "tsvectorin",
	3611: "tsvectorout",
	3612: "tsqueryin",
	3613: "tsqueryout",
	3638: "tsvectorsend",
	3639: "tsvectorrecv",
	3640: "tsquerysend",
	3641: "tsqueryrecv",
	3688: "ts_typanalyze",
	3736: "regconfigin",
	3737: "regconfigout",
	3738: "regconfigrecv",
	3739: "regconfigsend",
	3771: "regdictionaryin",
	3772: "regdictionaryout",
	3773: "regdictionaryrecv",
	3774: "regdictionarysend",
	3803: "jsonb_send",
	3804: "jsonb_out",
	3805: "jsonb_recv",
	3806: "jsonb_in",
	3816: "array_typanalyze",
	3834: "range_in",
	3835: "range_out",
	3836: "range_recv",
	3837: "range_send",
	3916: "range_typanalyze",
	4001: "jsonpath_in",
	4002: "jsonpath_recv",
	4003: "jsonpath_out",
	4004: "jsonpath_send",
	4084: "regnamespacein",
	4085: "regnamespaceout",
	4087: "regnamespacerecv",
	4088: "regnamespacesend",
	4092: "regroleout",
	4094: "regrolerecv",
	4095: "regrolesend",
	4098: "regrolein",
	4193: "regcollationin",
	4194: "regcollationout",
	4196: "regcollationrecv",
	4197: "regcollationsend",
	4231: "multirange_in",
	4232: "multirange_out",
	4233: "multirange_recv",
	4234: "multirange_send",
	4242: "multirange_typanalyze",
	5070: "xid8in",
	5081: "xid8out",
	5082: "xid8recv",
	5083: "xid8send",
	6098: "jsonb_subscript_handler",
	6179: "array_subscript_handler",
	6180: "raw_array_subscript_handler",
}

func pgTypeNumericTypes() []string {
	types := append([]string(nil), pgTypeTypes...)
	for _, i := range pgTypeRoutineColumns {
		types[i] = "OID"
	}
	return types
}
func pgTypeRawFacts(row []any) []any {
	result := append([]any(nil), row...)
	for _, i := range pgTypeRoutineColumns {
		id := row[i].(int64)
		result[i] = "-"
		if id != 0 {
			result[i] = pgTypeRoutineNames[id]
		}
	}
	return result
}
func TestReferencePGTypeBuiltins(t *testing.T) {
	pgTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Per-identity subtests preserve every oracle observation if one row differs.
		for _, row := range pgTypeBuiltinFacts {
			t.Run(fmt.Sprint(row[0], "_", row[1]), func(t *testing.T) {
				q := "SELECT " + pgTypeNumericProjection + " FROM pg_catalog.pg_type WHERE oid=$1::oid"
				step := fixture.Select(q, fixture.PGTypeColumns, pgTypeNumericTypes(), [][]any{row}, row[0])
				fixture.PgDescriptionRows(t, p, b, step)
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				for _, arg := range []any{row[0], nil, int64(0), row[0]} {
					step.Args = []any{arg}
					step.Rows = nil
					if arg == row[0] {
						step.Rows = [][]any{row}
					}
					fixture.PgDescriptionExecutorRows(t, p, b, held, step)
				}
				raw := pgTypeStep(b, "SELECT * FROM pg_catalog.pg_type WHERE oid=$1::oid", [][]any{pgTypeRawFacts(row)}, row[0])
				fixture.PgDescriptionRows(t, p, b, raw)
			})
		}
		// One ordered bag catches missing/duplicate companions independently of probes.
		ids := make([]string, len(pgTypeBuiltinFacts))
		for i, r := range pgTypeBuiltinFacts {
			ids[i] = fmt.Sprint(r[0])
		}
		s := fixture.Select("SELECT "+pgTypeNumericProjection+" FROM pg_type WHERE oid IN ("+strings.Join(ids, ",")+") ORDER BY oid", fixture.PGTypeColumns, pgTypeNumericTypes(), pgTypeBuiltinFacts)
		fixture.PgDescriptionRows(t, p, b, s)
	})
}

// Preserve actual database/sql descriptors beyond EOF/Close, not calls on closed Rows.
func pgTypeSavedMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 32 {
		t.Fatalf("pg_type descriptor width=%d", len(types))
	}
	for i, c := range types {
		scan := reflect.TypeFor[string]()
		switch pgTypeTypes[i] {
		case "OID", "INT2", "INT4":
			scan = reflect.TypeFor[int64]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		}
		if pg {
			switch pgTypeTypes[i] {
			case "INT2":
				scan = reflect.TypeFor[int16]()
			case "INT4":
				scan = reflect.TypeFor[int32]()
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "TEXT":
				scan = reflect.TypeFor[string]()
			default:
				scan = reflect.TypeFor[any]()
			}
		}
		nullable, known := c.Nullable()
		length, lengthKnown := c.Length()
		precision, scale, decimalKnown := c.DecimalSize()
		// Both adapters report unbounded TEXT length; other fields lack Length.
		wantLength, wantLengthKnown := int64(0), false
		if pgTypeTypes[i] == "TEXT" {
			wantLength, wantLengthKnown = int64(1<<63-1), true
		}
		// Nonfatal, full raw metadata is essential during oracle-first discovery.
		if c.Name() != fixture.PGTypeColumns[i] || c.DatabaseTypeName() != pgTypeTypes[i] || c.ScanType() != scan || (pg && (nullable || known)) || (!pg && (!known || nullable != (i >= 29))) || length != wantLength || lengthKnown != wantLengthKnown || precision != 0 || scale != 0 || decimalKnown {
			t.Errorf("column[%d] name=%q dbtype=%q scan=%v nullable=%v/%v length=%d/%v decimal=%d/%d/%v; want name=%q dbtype=%q scan=%v nativeNullable=%v length=%d/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), nullable, known, length, lengthKnown, precision, scale, decimalKnown, fixture.PGTypeColumns[i], pgTypeTypes[i], scan, i >= 29, wantLength, wantLengthKnown)
		}
	}
}
