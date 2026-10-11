package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathGcdLcmConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathGcdLcmBackends(t, p, func(t *testing.T, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE gl_source(id INTEGER,a INTEGER,b INTEGER,n NUMERIC(8,2),m NUMERIC(8,4))", 0))
		run(fixture.Exec("INSERT INTO gl_source VALUES(1,12,18,1.20,0.1800),(2,-8,12,-1.20,0.1800),(3,NULL,9,NULL,9)", 3))
		q := "SELECT id,gcd(a,b) AS gi,lcm(a,b) AS li,gcd(a::bigint,b::bigint) AS gb,lcm(a::bigint,b::bigint) AS lb,gcd(n,m) AS gn,lcm(n,m) AS ln FROM gl_source ORDER BY id"
		names := []string{"id", "gi", "li", "gb", "lb", "gn", "ln"}
		types := []string{"INT4", "INT4", "INT4", "INT8", "INT8", "NUMERIC", "NUMERIC"}
		rows := [][]any{{int64(1), int64(6), int64(36), int64(6), int64(36), "0.0600", "3.6000"}, {int64(2), int64(4), int64(24), int64(4), int64(24), "0.0600", "3.6000"}, {int64(3), nil, nil, nil, nil, nil, nil}}
		// Stored id metadata is distinct from computed metadata; ordinary fixture
		// asserts its name/type/value without applying the computed-only hook.
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(q, names, types, rows)
			s.Prepared = prepared
			run(s)
		}
		referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd(lcm(a,b),a) AS result FROM gl_source ORDER BY id", []string{"result"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(8)}, {nil}}))
		referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd(count(*),6::bigint) AS g,lcm(count(*),6::bigint) AS l FROM gl_source", []string{"g", "l"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(6)}}))
		run(fixture.Exec("CREATE TABLE gl_groups(a INTEGER,b INTEGER)", 0))
		run(fixture.Exec("INSERT INTO gl_groups VALUES(12,18),(18,12),(8,12)", 3))
		s := fixture.Select("SELECT gcd(a,b) AS g,count(*) AS c FROM gl_groups GROUP BY 1", []string{"g", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(6), int64(2)}, {int64(4), int64(1)}})
		s.Bag = true
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathGcdLcmMetadata(t, rows, b.PG, 1) }
		referenceMathGcdLcmStep(t, p, b, s)
		run(fixture.Exec("CREATE TABLE gl_copy(id INTEGER,g INTEGER,l INTEGER)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO gl_copy SELECT id,gcd(a,b),lcm(a,b) FROM gl_source ORDER BY id", 3)
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Select("SELECT id,g,l FROM gl_copy ORDER BY id", []string{"id", "g", "l"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(6), int64(36)}, {int64(1), int64(6), int64(36)}, {int64(2), int64(4), int64(24)}, {int64(2), int64(4), int64(24)}, {int64(3), nil, nil}, {int64(3), nil, nil}}))
		run(fixture.Exec("CREATE TABLE gl_update(id INTEGER,a INTEGER,b INTEGER,g INTEGER,l INTEGER)", 0))
		run(fixture.Exec("INSERT INTO gl_update SELECT id,a,b,0,0 FROM gl_source ORDER BY id", 3))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("UPDATE gl_update SET g=gcd(a,b),l=lcm(a,b)", 3)
			s.Prepared = prepared
			run(s)
			run(fixture.Select("SELECT id,g,l FROM gl_update ORDER BY id", []string{"id", "g", "l"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(6), int64(36)}, {int64(2), int64(4), int64(24)}, {int64(3), nil, nil}}))
		}
		run(fixture.Exec("CREATE TABLE gl_immutable(a INTEGER,b INTEGER,g INTEGER GENERATED ALWAYS AS (gcd(a,b)) STORED,CONSTRAINT gl_positive CHECK (lcm(a,b)>=0))", 0))
		run(fixture.Exec("INSERT INTO gl_immutable(a,b) VALUES(-12,18),(NULL,9)", 2))
		run(fixture.Exec("CREATE INDEX gl_expression ON gl_immutable(lcm(a,b))", 0))
		run(fixture.Select("SELECT a,b,g FROM gl_immutable ORDER BY a NULLS LAST", []string{"a", "b", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(-12), int64(18), int64(6)}, {nil, int64(9), nil}}))
		run(fixture.Exec("UPDATE gl_immutable SET a=-8,b=12 WHERE a=-12", 1))
		run(fixture.Select("SELECT g FROM gl_immutable ORDER BY g NULLS LAST", []string{"g"}, []string{"INT4"}, [][]any{{int64(4)}, {nil}}))
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{"INSERT INTO gl_immutable(a,b) VALUES(2147483647,2)", "UPDATE gl_immutable SET a=2147483647,b=2 WHERE a=-8"} {
				failure := referenceMathGcdLcmFailure(query, "22003", "integer out of range")
				failure.SelectRows, failure.Prepared = false, prepared
				run(failure)
				run(fixture.Select("SELECT a,b,g FROM gl_immutable ORDER BY a NULLS LAST", []string{"a", "b", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(-8), int64(12), int64(4)}, {nil, int64(9), nil}}))
			}
		}
		run(fixture.Exec("CREATE TABLE gl_demand(a INTEGER,b INTEGER,n INTEGER)", 0))
		referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd(a,b) AS g,lcm(a,b) AS l FROM gl_demand", []string{"g", "l"}, []string{"INT4", "INT4"}, nil))
		run(fixture.Exec("INSERT INTO gl_demand VALUES(2147483647,2,NULL)", 1))
		referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd(NULL::integer,lcm(a,b)) AS g,lcm(NULL::integer,lcm(a,b)) AS l FROM gl_demand", []string{"g", "l"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
		for _, name := range []string{"gcd", "lcm"} {
			referenceMathGcdLcmStep(t, p, b, referenceMathGcdLcmFailure("SELECT "+name+"(n,lcm(a,b)) FROM gl_demand", "22003", "integer out of range"))
		}
		for _, width := range []struct {
			cast, typ, max, message string
			safe, maxValue, zero    any
		}{
			{"integer", "INT4", "2147483647", "integer out of range", int64(12), int64(2147483647), int64(0)},
			{"bigint", "INT8", "9223372036854775807", "bigint out of range", int64(12), int64(9223372036854775807), int64(0)},
			{"numeric", "NUMERIC", strings.Repeat("9", 131072), "value overflows numeric format", "12", strings.Repeat("9", 131072), "0"},
		} {
			t.Run(width.cast+"_atomicity", func(t *testing.T) {
				run(fixture.Exec("CREATE TABLE gl_bad(id INTEGER,a "+width.cast+",b "+width.cast+")", 0))
				run(fixture.Exec("INSERT INTO gl_bad VALUES(1,12,18)", 1))
				run(fixture.Exec("INSERT INTO gl_bad VALUES(2,$1::"+width.cast+",2)", 1, width.max))
				run(fixture.Exec("CREATE TABLE gl_target(id INTEGER,v "+width.cast+")", 0))
				run(fixture.Exec("INSERT INTO gl_target VALUES(99,0)", 1))
				for _, prepared := range []bool{false, true} {
					for _, query := range []string{"INSERT INTO gl_target SELECT id,lcm(a,b) FROM gl_bad ORDER BY id", "UPDATE gl_bad SET a=lcm(a,b)"} {
						s := referenceMathGcdLcmFailure(query, "22003", width.message)
						s.SelectRows, s.Prepared = false, prepared
						run(s)
						run(fixture.Select("SELECT id,v FROM gl_target ORDER BY id", []string{"id", "v"}, []string{"INT4", width.typ}, [][]any{{int64(99), width.zero}}))
						var eighteen, two any = int64(18), int64(2)
						if width.typ == "NUMERIC" {
							eighteen, two = "18", "2"
						}
						run(fixture.Select("SELECT id,a,b FROM gl_bad ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", width.typ, width.typ}, [][]any{{int64(1), width.safe, eighteen}, {int64(2), width.maxValue, two}}))
					}
				}
				run(fixture.Exec("DROP TABLE gl_target", 0))
				run(fixture.Exec("DROP TABLE gl_bad", 0))
			})
		}
	})
}
