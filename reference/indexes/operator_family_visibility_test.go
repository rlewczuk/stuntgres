package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
)

// Independent finite native implementation facts for the existing complete
// released builtin inventories. PostgreSQL still checks every released row;
// native checks both admitted rows and absence of all other rows in that query.
// This helper is never used for owner-created/provider-backed DDL memberships.
func familyBuiltinVisible(s fixture.Step, pg bool) fixture.Step {
	if pg {
		return s
	}
	numbers := map[int64]bool{}
	for _, id := range []int64{350, 351, 354, 355, 356, 358, 359, 360, 377, 400, 425, 432, 441, 442, 443, 444, 445, 446, 447, 448, 449, 450, 451, 452, 453, 454, 455, 780, 836, 842, 926, 949, 972, 1078, 1080, 1092, 1107, 1314, 1315, 1358, 1688, 1693, 1696, 1697, 1769, 1954, 2039, 2045, 2166, 2180, 2233, 2234, 2794, 2960, 2963, 3251, 3409, 3410, 3411, 3412, 3414, 3416, 3418, 3514, 3515, 4044, 4045, 5096, 6413, 6414, 6415, 6416, 6417, 6418, 6419, 6420, 6421, 6422, 6423, 6424, 6425, 6426, 246, 253, 2188, 2189, 2190, 2191, 2192, 2193, 2194, 2195, 2344, 2357, 2370, 2383, 2526, 2533, 181, 3383, 3384, 3385, 3386} {
		numbers[id] = true
	}
	names := map[string]bool{}
	for _, name := range strings.Fields("btint2cmp btint4cmp btfloat4cmp btfloat8cmp btoidcmp btcharcmp btnamecmp bttextcmp cash_cmp hashtext hashint4extended hash_numeric hashint2extended hashint8extended hashfloat4extended hashfloat8extended hashoidextended hashcharextended hashnameextended hashtextextended hashint2 hashint4 hashfloat4 hashfloat8 hashoid hashchar hashname hash_numeric_extended macaddr_cmp btint8cmp network_cmp hashint8 hashbpcharextended bpcharcmp hashbpchar date_cmp time_cmp timestamptz_cmp interval_cmp timetz_cmp time_hash btboolcmp timetz_hash interval_hash numeric_cmp byteacmp timestamp_hash timestamp_cmp bttext_pattern_cmp btbpchar_pattern_cmp hashtid hashtidextended bttidcmp uuid_cmp uuid_hash pg_lsn_cmp time_hash_extended timetz_hash_extended timestamp_hash_extended uuid_hash_extended hashenumextended jsonb_hash_extended interval_hash_extended enum_cmp hashenum jsonb_cmp jsonb_hash xid8cmp hashbytea hashbyteaextended hashdate hashdateextended hashbool hashboolextended hashxid hashxidextended hashxid8 hashxid8extended hashcid hashcidextended timestamptz_hash timestamptz_hash_extended btnametextcmp bttextnamecmp btint48cmp btint84cmp btint24cmp btint42cmp btint28cmp btint82cmp btfloat48cmp btfloat84cmp date_cmp_timestamp date_cmp_timestamptz timestamp_cmp_date timestamptz_cmp_date timestamp_cmp_timestamptz timestamptz_cmp_timestamp int4mi brin_minmax_opcinfo brin_minmax_add_value brin_minmax_consistent brin_minmax_union") {
		names[name] = true
	}
	var rows [][]any
	for _, row := range s.Rows {
		admitted := false
		switch proc := row[len(row)-1].(type) {
		case int64:
			admitted = numbers[proc]
		case string:
			admitted = names[proc]
		}
		if admitted {
			rows = append(rows, row)
		}
	}
	s.Rows = rows
	return s
}
