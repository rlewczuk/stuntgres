package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsMCVItemsTypes(t *testing.T) {
	// Independently specified released type-output strings, not binary decoder
	// output or another backend's result. Presentation candidates await root oracle.
	cases := []struct{ name, typ, literal, text string }{
		{"bool", "boolean", "true", "t"},
		{"char", `"char"`, "'Z'", "Z"},
		{"int2", "int2", "-7", "-7"},
		{"int4", "int4", "-7", "-7"},
		{"int8", "int8", "1234567890123", "1234567890123"},
		{"oid", "oid", "4294967294", "4294967294"},
		{"xid8", "xid8", "'18446744073709551614'", "18446744073709551614"},
		{"lsn", "pg_lsn", "'FEDCBA98/76543210'", "FEDCBA98/76543210"},
		{"regclass", "regclass", "'123456'", "123456"},
		{"regtype", "regtype", "'23'", "integer"},
		{"regproc", "regproc", "'123456'", "123456"},
		{"regprocedure", "regprocedure", "'123456'", "123456"},
		{"regrole", "regrole", "'123456'", "123456"},
		{"regoper", "regoper", "'123456'", "123456"},
		{"regoperator", "regoperator", "'123456'", "123456"},
		{"regcollation", "regcollation", "'123456'", "123456"},
		{"regnamespace", "regnamespace", "'123456'", "123456"},
		{"regconfig", "regconfig", "'123456'", "123456"},
		{"regdictionary", "regdictionary", "'123456'", "123456"},
		{"regtype-unknown", "regtype", "'123456'", "123456"},
		{"regproc-symbol", "regproc", "'pg_mcv_list_items'", "pg_mcv_list_items"},
		{"regprocedure-symbol", "regprocedure", "'pg_mcv_list_items(pg_mcv_list)'", "pg_mcv_list_items(pg_mcv_list)"},
		{"regoper-symbol", "regoper", "(SELECT oid::regoper FROM pg_operator WHERE oprname='+' AND oprleft=23 AND oprright=23)", "pg_catalog.+"},
		{"regoperator-symbol", "regoperator", "'+(integer,integer)'", "+(integer,integer)"},
		{"regcollation-symbol", "regcollation", `'pg_catalog."C"'`, `"C"`},
		{"regnamespace-symbol", "regnamespace", "'pg_catalog'", "pg_catalog"},
		{"regconfig-symbol", "regconfig", "'simple'", "simple"},
		{"regdictionary-symbol", "regdictionary", "'simple'", "simple"},
		{"float4", "float4", "1.5", "1.5"},
		{"float8", "float8", "-2.25", "-2.25"},
		{"float4-nan", "float4", "'NaN'", "NaN"},
		{"float4-infinity", "float4", "'Infinity'", "Infinity"},
		{"float4-negative-infinity", "float4", "'-Infinity'", "-Infinity"},
		{"float8-nan", "float8", "'NaN'", "NaN"},
		{"float8-infinity", "float8", "'Infinity'", "Infinity"},
		{"float8-negative-infinity", "float8", "'-Infinity'", "-Infinity"},
		{"date", "date", "'2000-01-02'", "2000-01-02"},
		{"date-infinity", "date", "'infinity'", "infinity"},
		{"date-negative-infinity", "date", "'-infinity'", "-infinity"},
		{"time", "time", "'00:00:01'", "00:00:01"},
		{"timestamp", "timestamp", "'2000-01-01 00:00:01'", "2000-01-01 00:00:01"},
		{"timestamp-infinity", "timestamp", "'infinity'", "infinity"},
		{"timestamp-negative-infinity", "timestamp", "'-infinity'", "-infinity"},
		{"timestamptz", "timestamptz", "'2000-01-01 00:00:01+00'", "2000-01-01 00:00:01+00"},
		{"timestamptz-infinity", "timestamptz", "'infinity'", "infinity"},
		{"timetz", "timetz", "'00:00:01+02'", "00:00:01+02"},
		{"interval", "interval", "'2 months 3 days 4 seconds'", "2 mons 3 days 00:00:04"},
		{"uuid", "uuid", "'00112233-4455-6677-8899-aabbccddeeff'", "00112233-4455-6677-8899-aabbccddeeff"},
		{"mac", "macaddr", "'01:23:45:67:89:ab'", "01:23:45:67:89:ab"},
		{"tid", "tid", "'(65538,7)'", "(65538,7)"},
		{"name", "name", "'label'", "label"},
		{"text", "text", "'NULL'", "NULL"},
		{"empty", "text", "''", ""},
		{"varchar", "varchar(9)", "'hello'", "hello"},
		{"bpchar", "char(3)", "'x'", "x  "},
		{"bytea", "bytea", `'\x0001ff'`, `\x0001ff`},
		{"money", "money", "'12.34'", "$12.34"},
		{"numeric-scale", "numeric", "12.50", "12.50"},
		{"numeric-negative", "numeric", "-0.00001230", "-0.00001230"},
		{"numeric-zero", "numeric", "0.00", "0.00"},
		{"numeric-nan", "numeric", "'NaN'", "NaN"},
		{"numeric-infinity", "numeric", "'Infinity'", "Infinity"},
		{"numeric-negative-infinity", "numeric", "'-Infinity'", "-Infinity"},
		{"inet4", "inet", "'192.0.2.9/24'", "192.0.2.9/24"},
		{"cidr4", "cidr", "'192.0.2.0/24'", "192.0.2.0/24"},
		{"inet6", "inet", "'2001:db8::1/64'", "2001:db8::1/64"},
		{"cidr6", "cidr", "'2001:db8::/64'", "2001:db8::/64"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				for _, q := range []string{"SET TimeZone='UTC'", "SET DateStyle='ISO, MDY'", "SET IntervalStyle='postgres'", "SET bytea_output='hex'"} {
					s.run(fixture.Exec(q, 0))
				}
				mcvItemsTypeSetup(s, c.typ, c.literal)
				mcvItemsCheck(t, s, "mcv_item_type_s", mcvItemsExpected{[]any{c.text, "1"}, 1, 1})
			})
		})
	}
	for _, typ := range []string{"float4", "float8"} {
		t.Run(typ+"-zero-equality", func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				s.run(fixture.Exec("CREATE TABLE mcv_zero(a "+typ+",b INT)", 0))
				s.run(fixture.Exec("CREATE STATISTICS mcv_zero_s(mcv) ON a,b FROM mcv_zero", 0))
				s.run(fixture.Exec("INSERT INTO mcv_zero VALUES('-0',1),('0',1),('NaN',2),('NaN',2)", 4))
				s.run(fixture.Exec("ANALYZE mcv_zero", 0))
				items := mcvItemsRead(t, s.pair.Ctx, s, "mcv_zero_s")
				// Equal signed zeros have no portable chosen representative sign.
				for i := range items {
					if items[i].values[0].String == "-0" {
						items[i].values[0].String = "0"
						items[i].rawValues = "{0,1}"
					}
				}
				mcvItemsAssert(t, items, mcvItemsExpected{[]any{"0", "1"}, .5, .25}, mcvItemsExpected{[]any{"NaN", "2"}, .5, .25})
			})
		})
	}
}
