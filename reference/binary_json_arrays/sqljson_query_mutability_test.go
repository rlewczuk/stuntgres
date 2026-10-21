package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONQueryMutability(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
			columns := `a timestamp,b timestamptz,c timestamp,z timestamptz,d date,tm time,tz timetz`
			values := `TIMESTAMP '2018-02-20 18:34:56',TIMESTAMPTZ '2018-02-21 02:34:56+00',TIMESTAMP '2018-02-20 18:34:56',TIMESTAMPTZ '2018-02-21 02:34:56+00',DATE '2018-02-21',TIME '12:00',TIMETZ '12:00+00'`
			run(fixture.Exec(`CREATE TABLE sqjq_mutability_source(`+columns+`)`, 0))
			run(fixture.Exec(`INSERT INTO sqjq_mutability_source VALUES(`+values+`)`, 1))

			// Each operation already executes temporal items. Its property analysis
			// must preserve their status, including the filter's current item and
			// a filtered sequence subsequently used as a comparison operand.
			for _, tc := range []struct {
				name, path string
				filter     bool
			}{
				{"filter_current", `$a ? (@ == $b)`, true},
				{"lax_index", `$a[0] == $b`, false},
				{"lax_wildcard", `$a[*] == $b`, false},
				{"filter_operand", `($a ? (@ == $a)) == $b`, false},
				{"filter_lax_index", `$a ? (@[0] == $b)`, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					expression := `JSON_VALUE('{}','` + tc.path + `' PASSING a AS a,b AS b RETURNING boolean ERROR ON ERROR)`
					if tc.filter {
						expression = `JSON_EXISTS('{}','` + tc.path + `' PASSING a AS a,b AS b ERROR ON ERROR)`
					}
					for _, consumer := range []struct{ name, query, message string }{
						{"generated", `CREATE TABLE sqjq_mutability_reject_` + tc.name + `(a timestamp,b timestamptz,v boolean GENERATED ALWAYS AS (` + expression + `) STORED)`, `generation expression is not immutable`},
						{"index", `CREATE INDEX sqjq_mutability_reject_` + tc.name + ` ON sqjq_mutability_source((` + expression + `))`, `functions in index expression must be marked IMMUTABLE`},
					} {
						t.Run(consumer.name, func(t *testing.T) {
							// Both severities and every absent diagnostic field are
							// independent expectations, not native/PG parity alone.
							d := fixture.ControlsReferenceDiagnostic("42P17", consumer.message)
							for _, prepared := range []bool{false, true} {
								name := "direct"
								if prepared {
									name = "prepared"
								}
								t.Run(name, func(t *testing.T) {
									s := fixture.ErrorDiagnostic(consumer.query, d)
									s.Prepared, s.ErrorStage = prepared, "execute"
									fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
								})
							}
						})
					}

					q := `SELECT ` + expression + ` AS v FROM sqjq_mutability_source`
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					for _, zone := range []struct {
						name string
						want bool
					}{{"UTC", false}, {"America/Los_Angeles", true}, {"UTC", false}} {
						run(fixture.Exec(`SET TIME ZONE '`+zone.name+`'`, 0))
						s := sqljsonQueryOne(q, "BOOL", zone.want)
						sqljsonBoth(t, p, b, c, s)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
				})
			}

			// Immutable controls deliberately distinguish strict scalar-access
			// failure from lax scalar wrapping. COALESCE makes strict comparison
			// unknown and selected ON ERROR false explicit in the expected row.
			for _, tc := range []struct {
				name, expression string
				want             bool
			}{
				{"strict_index", `COALESCE(JSON_VALUE('{}','strict $a[0] == $b' PASSING a AS a,b AS b RETURNING boolean DEFAULT false ON ERROR),false)`, false},
				{"strict_wildcard", `COALESCE(JSON_VALUE('{}','strict $a[*] == $b' PASSING a AS a,b AS b RETURNING boolean DEFAULT false ON ERROR),false)`, false},
				{"strict_filter_index", `JSON_EXISTS('{}','strict $a ? (@[0] == $b)' PASSING a AS a,b AS b FALSE ON ERROR)`, false},
				{"same_zone_filter", `JSON_EXISTS('{}','$a ? (@ == $b)' PASSING a AS a,c AS b)`, true},
				{"same_zone_zoned_filter", `JSON_EXISTS('{}','$a ? (@ == $b)' PASSING b AS a,z AS b)`, true},
				{"same_zone_index", `JSON_VALUE('{}','$a[0] == $b' PASSING a AS a,c AS b RETURNING boolean)`, true},
				{"same_zone_filter_operand", `JSON_VALUE('{}','($a ? (@ == $a)) == $b' PASSING a AS a,c AS b RETURNING boolean)`, true},
				{"bare_date", `JSON_EXISTS('{}','$v' PASSING d AS v)`, true},
				{"bare_time", `JSON_EXISTS('{}','$v' PASSING tm AS v)`, true},
				{"bare_timetz", `JSON_EXISTS('{}','$v' PASSING tz AS v)`, true},
				{"bare_timestamp", `JSON_EXISTS('{}','$v' PASSING a AS v)`, true},
				{"bare_timestamptz", `JSON_EXISTS('{}','$v' PASSING b AS v)`, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						name := "direct"
						if prepared {
							name = "prepared"
						}
						t.Run(name, func(t *testing.T) {
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
							table := "sqjq_mutability_control_" + tc.name
							index := "sqjq_mutability_index_" + tc.name
							s := fixture.Exec(`CREATE TABLE `+table+`(`+columns+`,v boolean GENERATED ALWAYS AS (`+tc.expression+`) STORED)`, 0)
							s.Prepared = prepared
							run(s)
							run(fixture.Exec(`INSERT INTO `+table+`(a,b,c,z,d,tm,tz) VALUES(`+values+`)`, 1))
							sqljsonBoth(t, p, b, c, sqljsonQueryOne(`SELECT v FROM `+table, "BOOL", tc.want))
							s = fixture.Exec(`CREATE INDEX `+index+` ON sqjq_mutability_source((`+tc.expression+`))`, 0)
							s.Prepared = prepared
							run(s)
							sqljsonBoth(t, p, b, c, sqljsonQueryOne(`SELECT `+tc.expression+` AS v FROM sqjq_mutability_source`, "BOOL", tc.want))
							run(fixture.Exec(`DROP INDEX `+index, 0))
							run(fixture.Exec(`DROP TABLE `+table, 0))
						})
					}
				})
			}
		})
	}
}
