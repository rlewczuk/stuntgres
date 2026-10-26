package routine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestOperatorFamilySupportDeclarations(t *testing.T) {
	// Independent pinned PostgreSQL 18.6/180006 UTF8/C support_declarations
	// extraction plus the MACADDR declaration supplement. These are released
	// facts, not values obtained from the installed declarations or pg_proc
	// producer. Physical-only support functions remain lookup-only.
	want := [...]struct {
		oid, namespace ids.OID
		name, inputs   string
		result         ids.OID
		strict         bool
		volatility     expr.Volatility
	}{
		{246, 11, "btnametextcmp", "19,25", 23, true, expr.Immutable},
		{253, 11, "bttextnamecmp", "25,19", 23, true, expr.Immutable},
		{350, 11, "btint2cmp", "21,21", 23, true, expr.Immutable},
		{351, 11, "btint4cmp", "23,23", 23, true, expr.Immutable},
		{354, 11, "btfloat4cmp", "700,700", 23, true, expr.Immutable},
		{355, 11, "btfloat8cmp", "701,701", 23, true, expr.Immutable},
		{356, 11, "btoidcmp", "26,26", 23, true, expr.Immutable},
		{358, 11, "btcharcmp", "18,18", 23, true, expr.Immutable},
		{359, 11, "btnamecmp", "19,19", 23, true, expr.Immutable},
		{360, 11, "bttextcmp", "25,25", 23, true, expr.Immutable},
		{377, 11, "cash_cmp", "790,790", 23, true, expr.Immutable},
		{400, 11, "hashtext", "25", 23, true, expr.Immutable},
		{425, 11, "hashint4extended", "23,20", 20, true, expr.Immutable},
		{432, 11, "hash_numeric", "1700", 23, true, expr.Immutable},
		{441, 11, "hashint2extended", "21,20", 20, true, expr.Immutable},
		{442, 11, "hashint8extended", "20,20", 20, true, expr.Immutable},
		{443, 11, "hashfloat4extended", "700,20", 20, true, expr.Immutable},
		{444, 11, "hashfloat8extended", "701,20", 20, true, expr.Immutable},
		{445, 11, "hashoidextended", "26,20", 20, true, expr.Immutable},
		{446, 11, "hashcharextended", "18,20", 20, true, expr.Immutable},
		{447, 11, "hashnameextended", "19,20", 20, true, expr.Immutable},
		{448, 11, "hashtextextended", "25,20", 20, true, expr.Immutable},
		{449, 11, "hashint2", "21", 23, true, expr.Immutable},
		{450, 11, "hashint4", "23", 23, true, expr.Immutable},
		{451, 11, "hashfloat4", "700", 23, true, expr.Immutable},
		{452, 11, "hashfloat8", "701", 23, true, expr.Immutable},
		{453, 11, "hashoid", "26", 23, true, expr.Immutable},
		{454, 11, "hashchar", "18", 23, true, expr.Immutable},
		{455, 11, "hashname", "19", 23, true, expr.Immutable},
		{780, 11, "hash_numeric_extended", "1700,20", 20, true, expr.Immutable},
		{836, 11, "macaddr_cmp", "829,829", 23, true, expr.Immutable},
		{842, 11, "btint8cmp", "20,20", 23, true, expr.Immutable},
		{926, 11, "network_cmp", "869,869", 23, true, expr.Immutable},
		{949, 11, "hashint8", "20", 23, true, expr.Immutable},
		{972, 11, "hashbpcharextended", "1042,20", 20, true, expr.Immutable},
		{1078, 11, "bpcharcmp", "1042,1042", 23, true, expr.Immutable},
		{1080, 11, "hashbpchar", "1042", 23, true, expr.Immutable},
		{1092, 11, "date_cmp", "1082,1082", 23, true, expr.Immutable},
		{1107, 11, "time_cmp", "1083,1083", 23, true, expr.Immutable},
		{1314, 11, "timestamptz_cmp", "1184,1184", 23, true, expr.Immutable},
		{1315, 11, "interval_cmp", "1186,1186", 23, true, expr.Immutable},
		{1358, 11, "timetz_cmp", "1266,1266", 23, true, expr.Immutable},
		{1688, 11, "time_hash", "1083", 23, true, expr.Immutable},
		{1693, 11, "btboolcmp", "16,16", 23, true, expr.Immutable},
		{1696, 11, "timetz_hash", "1266", 23, true, expr.Immutable},
		{1697, 11, "interval_hash", "1186", 23, true, expr.Immutable},
		{1769, 11, "numeric_cmp", "1700,1700", 23, true, expr.Immutable},
		{1954, 11, "byteacmp", "17,17", 23, true, expr.Immutable},
		{2039, 11, "timestamp_hash", "1114", 23, true, expr.Immutable},
		{2045, 11, "timestamp_cmp", "1114,1114", 23, true, expr.Immutable},
		{2166, 11, "bttext_pattern_cmp", "25,25", 23, true, expr.Immutable},
		{2180, 11, "btbpchar_pattern_cmp", "1042,1042", 23, true, expr.Immutable},
		{2188, 11, "btint48cmp", "23,20", 23, true, expr.Immutable},
		{2189, 11, "btint84cmp", "20,23", 23, true, expr.Immutable},
		{2190, 11, "btint24cmp", "21,23", 23, true, expr.Immutable},
		{2191, 11, "btint42cmp", "23,21", 23, true, expr.Immutable},
		{2192, 11, "btint28cmp", "21,20", 23, true, expr.Immutable},
		{2193, 11, "btint82cmp", "20,21", 23, true, expr.Immutable},
		{2194, 11, "btfloat48cmp", "700,701", 23, true, expr.Immutable},
		{2195, 11, "btfloat84cmp", "701,700", 23, true, expr.Immutable},
		{2233, 11, "hashtid", "27", 23, true, expr.Immutable},
		{2234, 11, "hashtidextended", "27,20", 20, true, expr.Immutable},
		{2344, 11, "date_cmp_timestamp", "1082,1114", 23, true, expr.Immutable},
		{2357, 11, "date_cmp_timestamptz", "1082,1184", 23, true, expr.Stable},
		{2370, 11, "timestamp_cmp_date", "1114,1082", 23, true, expr.Immutable},
		{2383, 11, "timestamptz_cmp_date", "1184,1082", 23, true, expr.Stable},
		{2526, 11, "timestamp_cmp_timestamptz", "1114,1184", 23, true, expr.Stable},
		{2533, 11, "timestamptz_cmp_timestamp", "1184,1114", 23, true, expr.Stable},
		{2794, 11, "bttidcmp", "27,27", 23, true, expr.Immutable},
		{2960, 11, "uuid_cmp", "2950,2950", 23, true, expr.Immutable},
		{2963, 11, "uuid_hash", "2950", 23, true, expr.Immutable},
		{3251, 11, "pg_lsn_cmp", "3220,3220", 23, true, expr.Immutable},
		{3409, 11, "time_hash_extended", "1083,20", 20, true, expr.Immutable},
		{3410, 11, "timetz_hash_extended", "1266,20", 20, true, expr.Immutable},
		{3411, 11, "timestamp_hash_extended", "1114,20", 20, true, expr.Immutable},
		{3412, 11, "uuid_hash_extended", "2950,20", 20, true, expr.Immutable},
		{3414, 11, "hashenumextended", "3500,20", 20, true, expr.Immutable},
		{3416, 11, "jsonb_hash_extended", "3802,20", 20, true, expr.Immutable},
		{3418, 11, "interval_hash_extended", "1186,20", 20, true, expr.Immutable},
		{3514, 11, "enum_cmp", "3500,3500", 23, true, expr.Immutable},
		{3515, 11, "hashenum", "3500", 23, true, expr.Immutable},
		{4044, 11, "jsonb_cmp", "3802,3802", 23, true, expr.Immutable},
		{4045, 11, "jsonb_hash", "3802", 23, true, expr.Immutable},
		{5096, 11, "xid8cmp", "5069,5069", 23, true, expr.Immutable},
		{6413, 11, "hashbytea", "17", 23, true, expr.Immutable},
		{6414, 11, "hashbyteaextended", "17,20", 20, true, expr.Immutable},
		{6415, 11, "hashdate", "1082", 23, true, expr.Immutable},
		{6416, 11, "hashdateextended", "1082,20", 20, true, expr.Immutable},
		{6417, 11, "hashbool", "16", 23, true, expr.Immutable},
		{6418, 11, "hashboolextended", "16,20", 20, true, expr.Immutable},
		{6419, 11, "hashxid", "28", 23, true, expr.Immutable},
		{6420, 11, "hashxidextended", "28,20", 20, true, expr.Immutable},
		{6421, 11, "hashxid8", "5069", 23, true, expr.Immutable},
		{6422, 11, "hashxid8extended", "5069,20", 20, true, expr.Immutable},
		{6423, 11, "hashcid", "29", 23, true, expr.Immutable},
		{6424, 11, "hashcidextended", "29,20", 20, true, expr.Immutable},
		{6425, 11, "timestamptz_hash", "1184", 23, true, expr.Immutable},
		{6426, 11, "timestamptz_hash_extended", "1184,20", 20, true, expr.Immutable},
	}
	for _, w := range want {
		d := BuiltinRoutine(w.oid)
		if d == nil {
			t.Errorf("support routine %d (%s) has no executable declaration", w.oid, w.name)
			continue
		}
		identity := catalog.RoutineIdentity{ID: w.oid, Namespace: w.namespace, Name: w.name, Signature: catalog.ProcedureSignature(w.inputs)}
		if d.Identity != identity || d.Kind != catalog.FunctionRoutine || d.Result != w.result || d.Strict != w.strict || d.Volatility != w.volatility || d.ReturnsSet || d.Providers != functions.IndexSupportRoutineProvider {
			t.Errorf("support routine %d: got %+v; want identity %+v, function result %d, strict %t, volatility %v, scalar index-support provider", w.oid, *d, identity, w.result, w.strict, w.volatility)
		}
	}
	installed := 0
	for _, d := range BuiltinRoutineDeclarations {
		if d.Providers&functions.IndexSupportRoutineProvider != 0 {
			installed++
		}
	}
	if installed != len(want) {
		t.Errorf("installed index-support declarations = %d, independently recorded inventory = %d", installed, len(want))
	}
}
