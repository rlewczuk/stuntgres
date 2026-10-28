package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONStored(t *testing.T) {
	// Each admission witness has a fresh table so a rejected expression cannot
	// prevent observation of the other argument-conversion categories.
	for _, tc := range []struct {
		name, decl, expr string
		immutable        bool
	}{
		{"integer", "v int", `JSON_SCALAR(v)`, true},
		{"text", "v text", `JSON_SCALAR(v)`, true},
		{"array_integer", "v int", `JSON_ARRAY(v RETURNING text)`, true},
		{"object_text", "v text", `JSON_OBJECT('a':v RETURNING text)`, true},
		{"date", "v date", `JSON_SCALAR(v)`, false},
		{"timestamp", "v timestamp", `JSON_SCALAR(v)`, false},
		{"timestamptz", "v timestamptz", `JSON_ARRAY(v RETURNING text)`, false},
		{"sql_array", "v int[]", `JSON_SCALAR(v)`, false},
		{"composite", "v sqj_stored_pair", `JSON_SCALAR(v)`, false},
		{"predicate", "v text", `v IS JSON WITH UNIQUE KEYS`, true},
		{"json_input", "v text", `JSON(v WITH UNIQUE KEYS)`, true},
		{"serialize", "v text", `JSON_SERIALIZE(v)`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TYPE sqj_stored_pair AS (n int,s text)`, 0))
				run(fixture.Exec("CREATE TABLE sqj_stored("+tc.decl+")", 0))
				generated := `CREATE TABLE sqj_generated(` + tc.decl + `, j text GENERATED ALWAYS AS ((` + tc.expr + `)::text) STORED)`
				index := `CREATE INDEX sqj_index ON sqj_stored (((` + tc.expr + `)::text))`
				for _, q := range []string{generated, index} {
					t.Run(q, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						if tc.immutable {
							run(fixture.Exec(q, 0))
							return
						}
						message := "functions in index expression must be marked IMMUTABLE"
						if q == generated {
							message = "generation expression is not immutable"
						}
						s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42P17", message))
						s.ErrorStage = "execute"
						run(s)
					})
				}
			})
		})
	}
	t.Run("constant folding controls", func(t *testing.T) {
		sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				`CREATE TABLE sqj_const(v int,j text GENERATED ALWAYS AS (JSON_SCALAR(1)::text) STORED)`,
				`CREATE INDEX sqj_const_index ON sqj_const ((JSON_ARRAY('a' RETURNING text)))`,
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
		})
	})
	t.Run("consumers and atomic writes", func(t *testing.T) {
		sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE sqj_store(id int,doc text,j json DEFAULT JSON_OBJECT('a':1), CHECK(doc IS JSON WITH UNIQUE KEYS))`, 0))
			run(fixture.Exec(`INSERT INTO sqj_store(id,doc) VALUES(1,'{}')`, 1))
			run(fixture.Exec(`CREATE VIEW sqj_store_view AS SELECT id,JSON_ARRAY(JSON(doc) RETURNING text) AS v FROM sqj_store`, 0))
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT id,doc,j FROM sqj_store ORDER BY id`, []string{"id", "doc", "j"}, []string{"INT4", "TEXT", "JSON"}, [][]any{{int64(1), `{}`, `{"a" : 1}`}}))
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT id,v FROM sqj_store_view ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), `[{}]`}}))
			run(fixture.Exec(`CREATE TABLE sqj_atomic(id int,j json)`, 0))
			run(fixture.Exec(`INSERT INTO sqj_atomic VALUES(1,JSON('{}'))`, 1))
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
					d.Detail, d.Context = `Token "x" is invalid.`, "JSON data, line 1: x"
					s := fixture.ErrorDiagnostic(`INSERT INTO sqj_atomic VALUES(2,JSON($1::text)),(3,JSON($2::text))`, d, `[]`, `x`)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.Select(`SELECT id,j FROM sqj_atomic ORDER BY id`, []string{"id", "j"}, []string{"INT4", "JSON"}, [][]any{{int64(1), `{}`}}))
				})
			}
			run(fixture.Exec(`INSERT INTO sqj_atomic VALUES(4,JSON('[]'))`, 1))
			run(fixture.Select(`SELECT id,j FROM sqj_atomic ORDER BY id`, []string{"id", "j"}, []string{"INT4", "JSON"}, [][]any{{int64(1), `{}`}, {int64(4), `[]`}}))
		})
	})
	for _, tc := range []struct{ name, expression, code, message string }{
		{"malformed", `JSON(doc)::text`, "22P02", "invalid input syntax for type json"},
		{"unique", `JSON_OBJECT('a':1,k:2 WITH UNIQUE KEYS RETURNING text)`, "22030", `duplicate JSON object key value: "a"`},
		{"typmod", `JSON_ARRAY(id RETURNING varchar(3))`, "22001", "value too long for type character varying(3)"},
		{"domain", `JSON_ARRAY(id RETURNING sqj_atomic_domain)`, "23514", `value for domain sqj_atomic_domain violates check constraint "sqj_atomic_domain_check"`},
	} {
		t.Run("late write "+tc.name, func(t *testing.T) {
			sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE DOMAIN sqj_atomic_domain AS text CHECK (VALUE <> '[123]')`, 0))
				run(fixture.Exec(`CREATE TABLE sqj_late(id int,doc text,k text,v text)`, 0))
				run(fixture.Exec(`INSERT INTO sqj_late VALUES(1,'{}','b','prior'),(123,'x','a','prior')`, 2))
				check := fixture.Select(`SELECT id,doc,k,v FROM sqj_late ORDER BY id`, []string{"id", "doc", "k", "v"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), `{}`, "b", "prior"}, {int64(123), "x", "a", "prior"}})
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if tc.code == "22P02" {
					d.Detail = `Token "x" is invalid.`
					d.Context = `JSON data, line 1: x`
				}
				if tc.code == "23514" {
					d.Schema, d.DataType, d.Constraint = "public", "sqj_atomic_domain", "sqj_atomic_domain_check"
				}
				for _, prepared := range []bool{false, true} {
					for _, q := range []string{
						`UPDATE sqj_late SET v=` + tc.expression,
						`INSERT INTO sqj_late SELECT id+1000,doc,k,` + tc.expression + ` FROM sqj_late ORDER BY id`,
					} {
						s := fixture.ErrorDiagnostic(q, d)
						s.Prepared, s.ErrorStage = prepared, "execute"
						run(s)
						run(check)
					}
				}
				run(fixture.Exec(`UPDATE sqj_late SET v=JSON_ARRAY(1 RETURNING text)`, 2))
				run(fixture.Select(`SELECT v FROM sqj_late ORDER BY id`, []string{"v"}, []string{"TEXT"}, [][]any{{`[1]`}, {`[1]`}}))
			})
		})
	}
	for _, tc := range []struct{ q, message string }{
		{`CREATE TABLE sqj_subdefault(v json DEFAULT JSON_ARRAY(SELECT 1))`, "cannot use subquery in DEFAULT expression"},
		{`CREATE TABLE sqj_subcheck(v int CHECK (JSON_ARRAY(SELECT 1) IS NOT NULL))`, "cannot use subquery in check constraint"},
		{`CREATE TABLE sqj_subgenerated(v int,j json GENERATED ALWAYS AS (JSON_ARRAY(SELECT v)) STORED)`, "cannot use subquery in column generation expression"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				d := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
				// Released transform places this diagnostic on the constructor.
				for i := 0; i+10 <= len(tc.q); i++ {
					if tc.q[i:i+10] == "JSON_ARRAY" {
						d.Position = fixture.Position(i + 1)
						break
					}
				}
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		})
	}
}
