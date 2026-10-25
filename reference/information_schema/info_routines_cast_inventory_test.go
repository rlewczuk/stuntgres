package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The cast provider adds these real declarations to the previously represented
// builtin inventory. Names/prosrc are independent REL_18_6 pg_proc.dat facts;
// pg_proc.h supplies INTERNAL, invoker security and NULL proargnames defaults.
// No row is derived from native display metadata or observed backend output.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_proc.dat
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_proc.h
var irBuiltinCastFacts = []irBuiltinFact{
	{77, "int4", "chartoi4", "INTERNAL", "INVOKER"},
	{78, "char", "i4tochar", "INTERNAL", "INVOKER"},
	{235, "float8", "i2tod", "INTERNAL", "INVOKER"},
	{236, "float4", "i2tof", "INTERNAL", "INVOKER"},
	{237, "int2", "dtoi2", "INTERNAL", "INVOKER"},
	{238, "int2", "ftoi2", "INTERNAL", "INVOKER"},
	{311, "float8", "ftod", "INTERNAL", "INVOKER"},
	{312, "float4", "dtof", "INTERNAL", "INVOKER"},
	{313, "int4", "i2toi4", "INTERNAL", "INVOKER"},
	{314, "int2", "i4toi2", "INTERNAL", "INVOKER"},
	{316, "float8", "i4tod", "INTERNAL", "INVOKER"},
	{317, "int4", "dtoi4", "INTERNAL", "INVOKER"},
	{318, "float4", "i4tof", "INTERNAL", "INVOKER"},
	{319, "int4", "ftoi4", "INTERNAL", "INVOKER"},
	{401, "text", "rtrim1", "INTERNAL", "INVOKER"},
	{406, "text", "name_text", "INTERNAL", "INVOKER"},
	{407, "name", "text_name", "INTERNAL", "INVOKER"},
	{408, "bpchar", "name_bpchar", "INTERNAL", "INVOKER"},
	{409, "name", "bpchar_name", "INTERNAL", "INVOKER"},
	{480, "int4", "int84", "INTERNAL", "INVOKER"},
	{481, "int8", "int48", "INTERNAL", "INVOKER"},
	{482, "float8", "i8tod", "INTERNAL", "INVOKER"},
	{483, "int8", "dtoi8", "INTERNAL", "INVOKER"},
	{652, "float4", "i8tof", "INTERNAL", "INVOKER"},
	{653, "int8", "ftoi8", "INTERNAL", "INVOKER"},
	{668, "bpchar", "bpchar", "INTERNAL", "INVOKER"},
	{669, "varchar", "varchar", "INTERNAL", "INVOKER"},
	{714, "int2", "int82", "INTERNAL", "INVOKER"},
	{754, "int8", "int28", "INTERNAL", "INVOKER"},
	{860, "bpchar", "char_bpchar", "INTERNAL", "INVOKER"},
	{944, "char", "text_char", "INTERNAL", "INVOKER"},
	{946, "text", "char_text", "INTERNAL", "INVOKER"},
	{1079, "regclass", "text_regclass", "INTERNAL", "INVOKER"},
	{1174, "timestamptz", "date_timestamptz", "INTERNAL", "INVOKER"},
	{1178, "date", "timestamptz_date", "INTERNAL", "INVOKER"},
	{1200, "interval", "interval_scale", "INTERNAL", "INVOKER"},
	{1287, "oid", "i8tooid", "INTERNAL", "INVOKER"},
	{1288, "int8", "oidtoi8", "INTERNAL", "INVOKER"},
	{1316, "time", "timestamp_time", "INTERNAL", "INVOKER"},
	{1370, "interval", "time_interval", "INTERNAL", "INVOKER"},
	{1388, "timetz", "timestamptz_timetz", "INTERNAL", "INVOKER"},
	{1400, "name", "text_name", "INTERNAL", "INVOKER"},
	{1401, "varchar", "name_text", "INTERNAL", "INVOKER"},
	{1419, "time", "interval_time", "INTERNAL", "INVOKER"},
	{1683, "bit", "bitfromint4", "INTERNAL", "INVOKER"},
	{1684, "int4", "bittoint4", "INTERNAL", "INVOKER"},
	{1685, "bit", "bit", "INTERNAL", "INVOKER"},
	{1687, "varbit", "varbit", "INTERNAL", "INVOKER"},
	{1703, "numeric", "numeric", "INTERNAL", "INVOKER"},
	{1715, "cidr", "inet_to_cidr", "INTERNAL", "INVOKER"},
	{1740, "numeric", "int4_numeric", "INTERNAL", "INVOKER"},
	{1742, "numeric", "float4_numeric", "INTERNAL", "INVOKER"},
	{1743, "numeric", "float8_numeric", "INTERNAL", "INVOKER"},
	{1744, "int4", "numeric_int4", "INTERNAL", "INVOKER"},
	{1745, "float4", "numeric_float4", "INTERNAL", "INVOKER"},
	{1779, "int8", "numeric_int8", "INTERNAL", "INVOKER"},
	{1781, "numeric", "int8_numeric", "INTERNAL", "INVOKER"},
	{1782, "numeric", "int2_numeric", "INTERNAL", "INVOKER"},
	{1783, "int2", "numeric_int2", "INTERNAL", "INVOKER"},
	{1961, "timestamp", "timestamp_scale", "INTERNAL", "INVOKER"},
	{1967, "timestamptz", "timestamptz_scale", "INTERNAL", "INVOKER"},
	{1968, "time", "time_scale", "INTERNAL", "INVOKER"},
	{1969, "timetz", "timetz_scale", "INTERNAL", "INVOKER"},
	{2019, "time", "timestamptz_time", "INTERNAL", "INVOKER"},
	{2027, "timestamp", "timestamptz_timestamp", "INTERNAL", "INVOKER"},
	{2028, "timestamptz", "timestamp_timestamptz", "INTERNAL", "INVOKER"},
	{2029, "date", "timestamp_date", "INTERNAL", "INVOKER"},
	{2046, "time", "timetz_time", "INTERNAL", "INVOKER"},
	{2047, "timetz", "time_timetz", "INTERNAL", "INVOKER"},
	{2075, "bit", "bitfromint8", "INTERNAL", "INVOKER"},
	{2076, "int8", "bittoint8", "INTERNAL", "INVOKER"},
	{2557, "bool", "int4_bool", "INTERNAL", "INVOKER"},
	{2558, "int4", "bool_int4", "INTERNAL", "INVOKER"},
	{2580, "float8", "jsonb_float8", "INTERNAL", "INVOKER"},
	{3449, "numeric", "jsonb_numeric", "INTERNAL", "INVOKER"},
	{3450, "int2", "jsonb_int2", "INTERNAL", "INVOKER"},
	{3451, "int4", "jsonb_int4", "INTERNAL", "INVOKER"},
	{3452, "int8", "jsonb_int8", "INTERNAL", "INVOKER"},
	{3453, "float4", "jsonb_float4", "INTERNAL", "INVOKER"},
	{3556, "bool", "jsonb_bool", "INTERNAL", "INVOKER"},
	{3811, "money", "int4_cash", "INTERNAL", "INVOKER"},
	{3812, "money", "int8_cash", "INTERNAL", "INVOKER"},
	{3823, "numeric", "cash_numeric", "INTERNAL", "INVOKER"},
	{3824, "money", "numeric_cash", "INTERNAL", "INVOKER"},
	{4123, "macaddr8", "macaddrtomacaddr8", "INTERNAL", "INVOKER"},
	{4124, "macaddr", "macaddr8tomacaddr", "INTERNAL", "INVOKER"},
	{4280, "int4multirange", "multirange_constructor0", "INTERNAL", "INVOKER"},
	{4281, "int4multirange", "multirange_constructor1", "INTERNAL", "INVOKER"},
	{4283, "nummultirange", "multirange_constructor0", "INTERNAL", "INVOKER"},
	{4284, "nummultirange", "multirange_constructor1", "INTERNAL", "INVOKER"},
	{4295, "int8multirange", "multirange_constructor0", "INTERNAL", "INVOKER"},
	{4296, "int8multirange", "multirange_constructor1", "INTERNAL", "INVOKER"},
	{5071, "xid", "xid8toxid", "INTERNAL", "INVOKER"},
	{6367, "bytea", "int2_bytea", "INTERNAL", "INVOKER"},
	{6368, "bytea", "int4_bytea", "INTERNAL", "INVOKER"},
	{6369, "bytea", "int8_bytea", "INTERNAL", "INVOKER"},
	{6370, "int2", "bytea_int2", "INTERNAL", "INVOKER"},
	{6371, "int4", "bytea_int4", "INTERNAL", "INVOKER"},
	{6372, "int8", "bytea_int8", "INTERNAL", "INVOKER"},
}

func TestReferenceInfoRoutinesCastInventory(t *testing.T) {
	irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
		display := irBuiltinInventoryStep(irBuiltinCastFacts)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, display)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(display))
		identities := make([]string, 0, len(irBuiltinCastFacts))
		want := make([][]any, 0, len(irBuiltinCastFacts))
		for _, fact := range irBuiltinCastFacts {
			identities = append(identities, strconv.FormatUint(uint64(fact.oid), 10))
			want = append(want, []any{int64(fact.oid), true})
		}
		// All of these released declarations have unnamed arguments, including
		// typmod/explicitness inputs and the zero-argument multirange constructors.
		arguments := fixture.Select("SELECT oid::bigint AS oid,proargnames IS NULL AS unnamed FROM pg_catalog.pg_proc WHERE oid IN ("+strings.Join(identities, ",")+")", []string{"oid", "unnamed"}, []string{"INT8", "BOOL"}, want)
		arguments.Bag = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, arguments)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(arguments))
		// The provider also owns these three real installed conversion routines.
		// Resolve generated identities independently through regprocedure, never
		// information_schema.routines or allocator-pinned extension OIDs.
		// REL_18_6 contrib/citext/citext--1.4.sql declares these exact internal
		// symbols with invoker security and no argument labels.
		defer func() { _, _ = c.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS citext CASCADE") }()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION citext", 0))
		var installedNames, installedOIDs []string
		var installedRows, installedArguments [][]any
		for _, fact := range []struct{ input, source string }{
			{"bpchar", "rtrim1"}, {"boolean", "booltext"}, {"inet", "network_show"},
		} {
			identity := rsuIdentity(t, p, c, "public", "citext", fact.input)
			oid := strings.TrimPrefix(identity, "citext_")
			installedNames = append(installedNames, "'"+identity+"'")
			installedOIDs = append(installedOIDs, oid)
			installedRows = append(installedRows, []any{"public", identity, "citext", fact.source, "INTERNAL", "INVOKER"})
			installedArguments = append(installedArguments, []any{oid, true})
		}
		installed := fixture.Select(
			"SELECT specific_schema::text,specific_name::text,routine_name::text,routine_definition::text,external_language::text,security_type::text FROM information_schema.routines WHERE specific_schema='public' AND specific_name IN ("+strings.Join(installedNames, ",")+")",
			display.Columns, display.Types, installedRows)
		installed.Bag = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, installed)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(installed))
		unnamed := fixture.Select("SELECT oid::text AS oid,proargnames IS NULL AS unnamed FROM pg_catalog.pg_proc WHERE oid IN ("+strings.Join(installedOIDs, ",")+")", []string{"oid", "unnamed"}, []string{"TEXT", "BOOL"}, installedArguments)
		unnamed.Bag = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, unnamed)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(unnamed))
	})
}
