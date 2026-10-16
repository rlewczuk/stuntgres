package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashPartitionTypes(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE DOMAIN hp_number AS NUMERIC", 0))
		// Each SQL expression and remainder is independently specified from release support2.
		for n, c := range []struct {
			typ        string
			values     []string
			remainders []int
		}{
			{"BOOLEAN", []string{"false", "true"}, []int{0, 0}},
			{"SMALLINT", []string{"-1", "0", "1", "2"}, []int{5, 0, 0, 2}},
			{"BIGINT", []string{"-1", "0", "1", "2", "17", "'-9223372036854775808'", "'9223372036854775807'"}, []int{5, 0, 0, 2, 4, 7, 6}},
			{"OID", []string{"0", "1", "2", "42", "'4294967295'"}, []int{0, 0, 2, 2, 5}},
			{"XID", []string{"'0'", "'1'", "'2'"}, []int{0, 0, 2}},
			{"CID", []string{"'0'", "'1'", "'2'"}, []int{0, 0, 2}},
			{"XID8", []string{"'0'", "'1'", "'2'", "'9223372036854775808'", "'18446744073709551615'"}, []int{0, 0, 2, 7, 5}},
			{"NUMERIC", []string{"0", "-0.000", "'NaN'", "'Infinity'", "'-Infinity'", "1", "-1", "1.00", "0.1", "10000", "12345.6789"}, []int{7, 7, 0, 0, 0, 1, 1, 1, 7, 2, 6}},
			{"hp_number", []string{"0", "1", "0.1"}, []int{7, 1, 7}},
			{"TEXT", []string{"'abc'", "''", "'abcdefghi'", "'abcdefghij'", "'abcdefghijk'", "'abcdefghijkl'", "'é雪'"}, []int{5, 6, 3, 4, 3, 1, 6}},
			{"VARCHAR", []string{"'abc'", "''"}, []int{5, 6}},
			{"CHAR(6)", []string{"'abc'", "'abc   '", "''"}, []int{5, 5, 6}},
			{"NAME", []string{"'abc'", "''", "'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'"}, []int{5, 6, 5}},
			{"BYTEA", []string{`'\x616263'`, `'\x'`, `'\x0080ff'`}, []int{5, 6, 6}},
			{"UUID", []string{"'00000000-0000-0000-0000-000000000000'"}, []int{5}},
			{"REAL", []string{"'0'", "'-0'", "'1'", "'NaN'", "'Infinity'", "'-Infinity'"}, []int{0, 0, 5, 0, 7, 6}},
			{"DOUBLE PRECISION", []string{"'0'", "'-0'", "'1'", "'NaN'", "'Infinity'", "'-Infinity'"}, []int{0, 0, 5, 0, 7, 6}},
			{"DATE", []string{"'2000-01-01'", "'-Infinity'", "'Infinity'"}, []int{0, 6, 7}},
			{"TIME", []string{"'00:00'"}, []int{0}},
			{"TIMESTAMP", []string{"'2000-01-01'", "'-Infinity'", "'Infinity'"}, []int{0, 7, 6}},
			{"TIMESTAMPTZ", []string{"'2000-01-01 00:00+00'", "'-Infinity'", "'Infinity'"}, []int{0, 7, 6}},
			{"TIMETZ", []string{"'00:00+00'", "'12:00+02'", "'10:00+00'"}, []int{3, 5, 3}},
			{"INTERVAL", []string{"'0'", "'-Infinity'", "'Infinity'", "'1 month'", "'30 days'", "'720 hours'"}, []int{0, 5, 6, 3, 3, 3}},
			{"JSONB", []string{`'[]'`, `'{}'`, `'null'`, `'true'`, `'false'`, `'1'`, `'-1'`, `'[1]'`, `'[[]]'`, `'[{}]'`, `'{"a":1}'`, `'{"aa":1,"b":2}'`, `'[1,2]'`, `'[2,1]'`, `'{"b":2,"aa":1,"b":2}'`}, []int{0, 0, 1, 0, 0, 1, 1, 1, 3, 3, 4, 3, 3, 1, 3}},
		} {
			t.Run(c.typ, func(t *testing.T) {
				run := referenceHashRun(t, p, b, prepared)
				name := fmt.Sprintf("hp_type_%d", n)
				run(fixture.Exec("CREATE TABLE "+name+"(k "+c.typ+",id INT) PARTITION BY HASH(k)", 0))
				referenceHashChildren(run, name, 8)
				for i, value := range c.values {
					run(fixture.Exec(fmt.Sprintf("INSERT INTO %s VALUES(%s,%d)", name, value, i), 1))
				}
				for r := range 8 {
					var want [][]any
					for i, expected := range c.remainders {
						if expected == r {
							want = append(want, []any{int64(i)})
						}
					}
					run(fixture.Select(fmt.Sprintf("SELECT id FROM %s_%d ORDER BY id", name, r), []string{"id"}, []string{"INT4"}, want))
				}
			})
		}
		for n, key := range []string{"k text_pattern_ops", "k bpchar_ops", "k COLLATE \"C\" text_ops"} {
			name := fmt.Sprintf("hp_class_%d", n)
			run(fixture.Exec("CREATE TABLE "+name+"(k TEXT,id INT) PARTITION BY HASH("+key+")", 0))
			referenceHashChildren(run, name, 8)
			run(fixture.Exec("INSERT INTO "+name+" VALUES('abc',1)", 1))
			run(fixture.Select("SELECT id FROM "+name+"_5", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
		t.Run("internal_char_column_boundary", func(t *testing.T) {
			run := referenceHashRun(t, p, b, prepared)
			query := `CREATE TABLE hp_internal_char(k "char",id INT) PARTITION BY HASH(k)`
			if !b.PG {
				s := referenceRangeError(query, "0A000", "unsupported column type", fixture.Diagnostic{Position: referenceRangePosition(query, `"char"`)})
				run(s)
				return
			}
			// PostgreSQL owns CHAR columns; native deliberately owns only its
			// scalar/string-role provider, whose signed hashes have unit vectors.
			run(fixture.Exec(query, 0))
			referenceHashChildren(run, "hp_internal_char", 8)
			run(fixture.Exec(`INSERT INTO hp_internal_char VALUES(E'\\200',0),(E'\\377',1)`, 2))
			run(fixture.Select("SELECT id FROM hp_internal_char_3", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT id FROM hp_internal_char_5", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	})
}
