package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONTableSources(t *testing.T) {
	t.Run("lateral namespace", func(t *testing.T) {
		jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE jt_docs(id int PRIMARY KEY,doc jsonb)`, 0))
			run(fixture.Exec(`INSERT INTO jt_docs VALUES(1,'[10,20]'),(2,'[]'),(3,'[30]')`, 3))
			for _, lateral := range []string{"", "LATERAL "} {
				sqljsonBoth(t, p, b, c, fixture.Select(`SELECT d.id,j.* FROM jt_docs d CROSS JOIN `+lateral+`JSON_TABLE(d.doc,'$[*]' COLUMNS(o FOR ORDINALITY,v int PATH '$')) AS j ORDER BY d.id,o`, []string{"id", "o", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(10)}, {int64(1), int64(2), int64(20)}, {int64(3), int64(1), int64(30)}}))
			}
			sqljsonBoth(t, p, b, c, jsonTableMetadata(fixture.Select(`SELECT d.id,j.* FROM jt_docs d LEFT JOIN JSON_TABLE(d.doc,'$[*]' COLUMNS(o FOR ORDINALITY,v int PATH '$',e bool EXISTS PATH '$')) j ON true ORDER BY d.id,o`, []string{"id", "o", "v", "e"}, []string{"INT4", "INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(1), int64(10), true}, {int64(1), int64(2), int64(20), true}, {int64(2), nil, nil, nil}, {int64(3), int64(1), int64(30), true}}), b.PG, false, true, true, true))
			sqljsonBoth(t, p, b, c, fixture.Select(`SELECT j::text AS row,j.x FROM JSON_TABLE('[7]','$[*]' COLUMNS(a int PATH '$')) AS j(x)`, []string{"row", "x"}, []string{"TEXT", "INT4"}, [][]any{{`(7)`, int64(7)}}))
			sqljsonBoth(t, p, b, c, fixture.Select(`SELECT json_table.a FROM JSON_TABLE('{"a":7}','$' COLUMNS(a int))`, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			for _, join := range []string{"RIGHT", "FULL"} {
				t.Run(join, func(t *testing.T) {
					q := `SELECT j.v FROM jt_docs d ` + join + ` JOIN JSON_TABLE(d.doc,'$[*]' COLUMNS(v int PATH '$')) j ON true`
					d := fixture.ControlsReferenceDiagnostic("42P10", `invalid reference to FROM-clause entry for table "d"`)
					d.Detail = `The combining JOIN type must be INNER or LEFT for a LATERAL reference.`
					d.Position = fixture.Position(strings.Index(q, "d.doc") + 1)
					sqljsonDiagnostic(t, p, b, c, q, d, true)
				})
				sqljsonBoth(t, p, b, c, fixture.Select(`SELECT count(*) AS v FROM jt_docs d `+join+` JOIN JSON_TABLE('[1]','$[*]' COLUMNS(v int PATH '$')) j ON true`, []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
			}
			sqljsonBoth(t, p, b, c, fixture.Select(`WITH j AS (SELECT * FROM JSON_TABLE('[2,3]','$[*]' COLUMNS(v int PATH '$'))) SELECT v FROM (SELECT v FROM j) s ORDER BY v`, []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
			run(fixture.Exec(`CREATE TABLE jt_ctas AS SELECT * FROM JSON_TABLE('[2,3]','$[*]' COLUMNS(v int PATH '$'))`, 2))
			sqljsonBoth(t, p, b, c, fixture.Select(`SELECT v FROM jt_ctas ORDER BY v`, []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
		})
	})
	t.Run("DML source consumers", func(t *testing.T) {
		jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) {
				// Existing public fixture.MergeRun convention: lib/pq does not
				// decode MERGE's command tag, although both mutations happen.
				// Keep native affected2 and the exact final-state witness below.
				if b.PG && strings.HasPrefix(s.Query, "MERGE ") {
					s.Affected = 0
				}
				if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }) {
					t.FailNow()
				}
			}
			run(fixture.Exec(`CREATE TABLE jt_insert(r int,id int,ao int,av int,bo int,bv text)`, 0))
			run(fixture.Exec(`INSERT INTO jt_insert SELECT * FROM JSON_TABLE($1::jsonb,'$[*]'`+jsonTableSiblingColumns+`)`, 5, jsonTableSiblingDocument))
			run(jsonTableSiblingStep(`SELECT * FROM jt_insert`))
			run(fixture.Exec(`CREATE TABLE jt_target(id int PRIMARY KEY,v int)`, 0))
			run(fixture.Exec(`INSERT INTO jt_target VALUES(1,0),(2,0),(3,0)`, 3))
			source := `JSON_TABLE('[{"id":1,"v":10},{"id":2,"v":20}]','$[*]' COLUMNS(id int,v int)) j`
			// Check the exact independent source bag before DML, rather than masking
			// a fixture/source bug by accepting an observed zero affected count.
			sqljsonBoth(t, p, b, c, fixture.Select(`SELECT j.id,j.v FROM `+source+` ORDER BY j.id`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			run(fixture.Exec(`UPDATE jt_target t SET v=j.v FROM `+source+` WHERE t.id=j.id`, 2))
			run(fixture.Select(`SELECT id,v FROM jt_target ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(0)}}))
			run(fixture.Exec(`DELETE FROM jt_target t USING `+source+` WHERE t.id=j.id`, 2))
			run(fixture.Select(`SELECT id,v FROM jt_target`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(0)}}))
			run(fixture.Exec(`MERGE INTO jt_target t USING JSON_TABLE('[{"id":3,"v":30},{"id":4,"v":40}]','$[*]' COLUMNS(id int,v int)) j ON t.id=j.id WHEN MATCHED THEN UPDATE SET v=j.v WHEN NOT MATCHED THEN INSERT(id,v) VALUES(j.id,j.v)`, 2))
			run(fixture.Select(`SELECT id,v FROM jt_target ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}, {int64(4), int64(40)}}))
			q := `UPDATE jt_target t SET v=j.v FROM JSON_TABLE(t.v::text,'$' COLUMNS(v int PATH '$')) j`
			d := fixture.ControlsReferenceDiagnostic("42P10", `invalid reference to FROM-clause entry for table "t"`)
			d.Hint = `There is an entry for table "t", but it cannot be referenced from this part of the query.`
			d.Position = fixture.Position(strings.Index(q, "t.v") + 1)
			sqljsonDiagnostic(t, p, b, c, q, d, true)
		})
	})
}
