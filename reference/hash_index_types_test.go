package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceHashIndexTypes(t *testing.T) {
	// IDs are independent equality witnesses, not backend-derived goldens. Each
	// representation pair must hash alike if its selected class considers it equal.
	for _, tc := range []struct {
		name, typ, values, probe string
		classes                  []string
		ids                      []int64
	}{
		{"bool", "BOOL", "true,true,false,NULL", "true", []string{"bool_ops"}, []int64{1, 2}},
		{"bytea", "BYTEA", `'\x0080ff','\x0080ff','\x00',NULL`, `'\x0080ff'::bytea`, []string{"bytea_ops"}, []int64{1, 2}},
		{"char", `"char"`, "'a','a','b',NULL", `'a'::"char"`, []string{"char_ops"}, []int64{1, 2}},
		{"name", "NAME", "'alpha','alpha','beta',NULL", "'alpha'::name", []string{"name_ops"}, []int64{1, 2}},
		{"int2", "SMALLINT", "1,1,2,NULL", "1::smallint", []string{"int2_ops"}, []int64{1, 2}},
		{"int4", "INT", "1,1,2,NULL", "1", []string{"int4_ops", "oid_ops"}, []int64{1, 2}},
		{"int8", "BIGINT", "1,1,2,NULL", "1::bigint", []string{"int8_ops"}, []int64{1, 2}},
		{"text", "TEXT", "'a','a','a ',NULL", "'a'::text", []string{"text_ops", "text_pattern_ops", "varchar_ops", "varchar_pattern_ops", "bpchar_ops", "bpchar_pattern_ops"}, []int64{1, 2}},
		{"varchar", "VARCHAR(8)", "'a','a','a ',NULL", "'a'::varchar", []string{"text_ops", "varchar_ops", "text_pattern_ops", "varchar_pattern_ops", "bpchar_ops", "bpchar_pattern_ops"}, []int64{1, 2}},
		{"bpchar", "CHAR(4)", "'a','a ','b',NULL", "'a'::char(4)", []string{"bpchar_ops", "bpchar_pattern_ops"}, []int64{1, 2}},
		{"oid", "OID", "1,1,2,NULL", "1::oid", []string{"oid_ops"}, []int64{1, 2}},
		{"float4-zero", "REAL", "'0','-0','1',NULL", "'0'::real", []string{"float4_ops"}, []int64{1, 2}},
		{"float4-nan", "REAL", "'NaN','NaN','1',NULL", "'NaN'::real", []string{"float4_ops"}, []int64{1, 2}},
		{"float8-zero", "DOUBLE PRECISION", "'0','-0','1',NULL", "'0'::double precision", []string{"float8_ops"}, []int64{1, 2}},
		{"float8-nan", "DOUBLE PRECISION", "'NaN','NaN','1',NULL", "'NaN'::double precision", []string{"float8_ops"}, []int64{1, 2}},
		{"numeric", "NUMERIC", "1,1.00,2,NULL", "1.000::numeric", []string{"numeric_ops"}, []int64{1, 2}},
		{"numeric-zero", "NUMERIC", "0,-0.00,2,NULL", "0.000::numeric", []string{"numeric_ops"}, []int64{1, 2}},
		{"numeric-nan", "NUMERIC", "'NaN','NaN',2,NULL", "'NaN'::numeric", []string{"numeric_ops"}, []int64{1, 2}},
		{"date", "DATE", "'2020-02-29','2020-02-29','2020-03-01',NULL", "'2020-02-29'::date", []string{"date_ops"}, []int64{1, 2}},
		{"time", "TIME", "'12:00:00','12:00:00.000','13:00:00',NULL", "'12:00:00'::time", []string{"time_ops"}, []int64{1, 2}},
		{"timestamp", "TIMESTAMP", "'2020-02-29 12:00','2020-02-29 12:00:00','2020-03-01 12:00',NULL", "'2020-02-29 12:00'::timestamp", []string{"timestamp_ops"}, []int64{1, 2}},
		{"timestamptz", "TIMESTAMPTZ", "'2020-02-29 12:00+00','2020-02-29 13:00+01','2020-03-01 12:00+00',NULL", "'2020-02-29 12:00+00'::timestamptz", []string{"timestamptz_ops"}, []int64{1, 2}},
		{"timetz", "TIMETZ", "'12:00+00','12:00+00','13:00+00',NULL", "'12:00+00'::timetz", []string{"timetz_ops"}, []int64{1, 2}},
		{"interval", "INTERVAL", "'1 mon','30 days','31 days',NULL", "'30 days'::interval", []string{"interval_ops"}, []int64{1, 2}},
		{"uuid", "UUID", "'12345678-1234-1234-1234-123456789abc','12345678-1234-1234-1234-123456789abc','12345678-1234-1234-1234-123456789abd',NULL", "'12345678-1234-1234-1234-123456789abc'::uuid", []string{"uuid_ops"}, []int64{1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				// Genuine provider catalog control: native storage excludes
				// internal CHAR; central provider units cover its native binding.
				if tc.name == "char" && !b.pg {
					return
				}
				run(referenceExec("CREATE TABLE t(id INT,v "+tc.typ+")", 0))
				// Values are deliberately separate INSERT statements: no fixture
				// normalization can silently rewrite a representation pair.
				values := strings.Split(tc.values, ",")
				for i, value := range values {
					run(referenceExec(fmt.Sprintf("INSERT INTO t VALUES(%d,%s)", i+1, value), 1))
				}
				rows := make([][]any, len(tc.ids))
				for i, id := range tc.ids {
					rows[i] = []any{id}
				}
				check := func() {
					s := referenceSelect("SELECT id FROM t WHERE v="+tc.probe+" ORDER BY id", []string{"id"}, []string{"INT4"}, rows)
					run(s)
					run(referencePrepared(s))
					run(referenceSelect("SELECT id FROM t WHERE v IS NULL", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}))
				}
				check() // Ordinary evaluator is an independent pre-index baseline.
				for _, class := range append([]string{""}, tc.classes...) {
					key := "v"
					if class != "" {
						key += " pg_catalog." + class
					}
					run(referenceExec("CREATE INDEX ix ON t USING hash("+key+")", 0))
					check()
					run(referenceExec("UPDATE t SET v="+tc.probe+" WHERE id=3", 1))
					run(referenceSelect("SELECT id FROM t WHERE v="+tc.probe+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					run(referenceExec("UPDATE t SET v="+values[2]+" WHERE id=3", 1))
					run(referenceExec("DROP INDEX ix", 0))
				}
			})
		})
	}
	t.Run("domain-enum-regclass", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
			for _, s := range []referenceStep{
				referenceExec("CREATE DOMAIN hash_number AS NUMERIC", 0),
				referenceExec("CREATE TYPE hash_mood AS ENUM('calm','busy')", 0),
				referenceExec("CREATE DOMAIN hash_mood_domain AS hash_mood", 0),
				referenceExec("CREATE TABLE hash_type_target(n INT)", 0),
				referenceExec("CREATE TABLE t(id INT,d hash_number,e hash_mood_domain,r OID)", 0),
				referenceExec("INSERT INTO t VALUES(1,1,'calm',1),(2,1.00,'calm',1),(3,2,'busy',2),(4,NULL,NULL,NULL)", 4),
			} {
				run(s)
			}
			for _, tc := range []struct{ key, class, predicate string }{
				{"d", "numeric_ops", "d=1::numeric"}, {"e", "enum_ops", "e::hash_mood='calm'::hash_mood"}, {"r", "oid_ops", "r=1::oid"},
			} {
				for _, explicit := range []bool{false, true} {
					key := tc.key
					if explicit {
						key += " " + tc.class
					}
					run(referencePrepared(referenceExec("CREATE INDEX ix ON t USING hash("+key+")", 0)))
					s := referenceSelect("SELECT id FROM t WHERE "+tc.predicate+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
					run(s)
					run(referencePrepared(s))
					run(referenceExec("DROP INDEX ix", 0))
				}
			}
			// Genuine PG stored-REGCLASS control, not a native storage expansion.
			// Shared OID values above cover the existing stored representation.
			if b.pg {
				run(referenceExec("CREATE TABLE regclass_keys(id INT,r REGCLASS)", 0))
				run(referenceExec("INSERT INTO regclass_keys VALUES(1,'hash_type_target'),(2,'hash_type_target'),(3,'t')", 3))
				run(referenceExec("CREATE INDEX regclass_hash ON regclass_keys USING hash(r oid_ops)", 0))
				run(referenceSelect("SELECT id FROM regclass_keys WHERE r='hash_type_target'::regclass ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			}
		})
	})
	t.Run("mixed-provider-fallback", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
			run(referenceExec("CREATE TABLE t(n INT,s TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'a'),(2,'a '),(2,'b'),(NULL,NULL)", 4))
			run(referenceExec("CREATE INDEX hn ON t USING hash(n)", 0))
			run(referenceExec("CREATE INDEX hs ON t USING hash(s bpchar_ops)", 0))
			for _, predicate := range []string{"n=2::bigint", "n=2.00::numeric", "2::bigint=n", "n=2 OR n IS NULL", "NOT(n=1)"} {
				rows := [][]any{{int64(2), "a "}, {int64(2), "b"}}
				if predicate == "n=2 OR n IS NULL" {
					rows = append(rows, []any{nil, nil})
				}
				s := hashIndexBag("SELECT n,s FROM t WHERE "+predicate, rows)
				run(s)
				run(referencePrepared(s))
			}
			for _, predicate := range []string{"s='a'::text", "s='a'::name", "'a'::name=s"} {
				s := hashIndexBag("SELECT n,s FROM t WHERE "+predicate, [][]any{{int64(1), "a"}})
				run(s)
				run(referencePrepared(s))
			}
		})
	})
}
