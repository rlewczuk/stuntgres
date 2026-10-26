package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBrinIndexTypes(t *testing.T) {
	brinIndexDomainTypes(t)
	databaseTypes := map[string]string{
		"bytea": "BYTEA", "char": "CHAR", "name": "NAME", "int2": "INT2", "int4": "INT4", "int8": "INT8",
		"text": "TEXT", "varchar": "VARCHAR", "oid": "OID", "tid": "TID", "float4": "FLOAT4", "float8": "FLOAT8",
		"macaddr": "MACADDR", "macaddr8": "MACADDR8", "inet": "INET", "cidr": "CIDR", "bpchar": "BPCHAR",
		"date": "DATE", "time": "TIME", "timestamp": "TIMESTAMP", "timestamptz": "TIMESTAMPTZ",
		"interval": "INTERVAL", "timetz": "TIMETZ", "bit": "BIT", "varbit": "VARBIT", "numeric": "NUMERIC", "uuid": "UUID",
	}
	for _, tc := range []struct {
		name, typ, value, class string
		explicitOnly            bool
	}{
		{"bytea", "BYTEA", `'\x0080ff'`, "bytea_minmax_ops", false},
		{"char", `"char"`, "'a'", "char_minmax_ops", false},
		{"name", "NAME", "'alpha'", "name_minmax_ops", false},
		{"int2", "SMALLINT", "4", "int2_minmax_ops", false},
		{"int4", "INT", "4", "int4_minmax_ops", false},
		{"int8", "BIGINT", "4", "int8_minmax_ops", false},
		{"text", "TEXT", "'alpha'", "text_minmax_ops", false},
		{"varchar", "VARCHAR(8)", "'alpha'", "text_minmax_ops", false},
		{"oid", "OID", "4", "oid_minmax_ops", false},
		{"tid", "TID", "'(1,2)'", "tid_minmax_ops", false},
		{"float4", "REAL", "'NaN'", "float4_minmax_ops", false},
		{"float8", "DOUBLE PRECISION", "'NaN'", "float8_minmax_ops", false},
		{"macaddr", "MACADDR", "'08:00:2b:01:02:03'", "macaddr_minmax_ops", false},
		{"macaddr8", "MACADDR8", "'08:00:2b:01:02:03:04:05'", "macaddr8_minmax_ops", false},
		{"inet", "INET", "'192.0.2.1'", "inet_minmax_ops", true},
		{"cidr", "CIDR", "'192.0.2.0/24'", "inet_minmax_ops", true},
		{"bpchar", "CHAR(4)", "'a '", "bpchar_minmax_ops", false},
		{"date", "DATE", "'2020-02-29'", "date_minmax_ops", false},
		{"time", "TIME", "'12:34:56'", "time_minmax_ops", false},
		{"timestamp", "TIMESTAMP", "'2020-02-29 12:34:56'", "timestamp_minmax_ops", false},
		{"timestamptz", "TIMESTAMPTZ", "'2020-02-29 12:34:56+00'", "timestamptz_minmax_ops", false},
		{"interval", "INTERVAL", "'1 mon'", "interval_minmax_ops", false},
		{"timetz", "TIMETZ", "'12:34:56+00'", "timetz_minmax_ops", false},
		{"bit", "BIT(3)", "B'101'", "bit_minmax_ops", false},
		{"varbit", "VARBIT(8)", "B'101'", "varbit_minmax_ops", false},
		{"numeric", "NUMERIC", "1.00", "numeric_minmax_ops", false},
		{"uuid", "UUID", "'12345678-1234-1234-1234-123456789abc'", "uuid_minmax_ops", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				if tc.name == "char" && !b.PG {
					hashIndexSemanticFailure(t, p, b, run, `CREATE TABLE t(id INT,v "char")`, fixture.Diagnostic{Code: "0A000", Message: "unsupported column type", Position: "25"})
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='t'", []string{"relname"}, []string{"NAME"}, nil))
					return
				}
				run(fixture.Exec("CREATE TABLE t(id INT,v "+tc.typ+")", 0))
				metadata := fixture.Select("SELECT v FROM t WHERE id=0", []string{"v"}, []string{databaseTypes[tc.name]}, nil)
				if tc.name == "macaddr8" && b.PG {
					// lib/pq lacks OID 774 in its DatabaseTypeName table;
					// retain the independent raw codec family and native name.
					metadata.RawTypes = []string{"MACADDR8"}
					metadata.Types = []string{""}
				}
				run(metadata)
				run(fixture.Prepared(metadata))
				keys := []string{"v pg_catalog." + tc.class}
				if !tc.explicitOnly {
					keys = append(keys, "v")
				}
				for _, key := range keys {
					run(fixture.Exec("CREATE INDEX empty_brin ON t USING brin("+key+")", 0))
					run(fixture.Exec("DROP INDEX empty_brin", 0))
				}
				run(fixture.Exec("INSERT INTO t VALUES(1,"+tc.value+"),(2,"+tc.value+"),(3,NULL)", 3))
				for _, key := range keys {
					run(fixture.Prepared(fixture.Exec("CREATE INDEX bn ON t USING brin("+key+") WITH(pages_per_range=2)", 0)))
					s := fixture.Select("SELECT id FROM t WHERE v="+tc.value+"::"+tc.typ+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
					run(s)
					run(fixture.Prepared(s))
					run(fixture.Select("SELECT id FROM t WHERE v IS NULL", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
					run(fixture.Exec("DROP INDEX bn", 0))
				}
			})
		})
	}
}
