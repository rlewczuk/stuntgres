package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeAlterValues(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE TABLE holder(k integer,v pair)`)
				run(fixture.Exec(`INSERT INTO holder VALUES (1,$1),(2,NULL),(3,'(,)')`, 3, "(7,x)"))
				check := func(w [][]any) {
					fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, w, prepared)
				}
				run(fixture.Exec(`ALTER TYPE pair ADD ATTRIBUTE extra text`, 0))
				check([][]any{{"(7,x,)"}, {nil}, {"(,,)"}})
				run(fixture.Select(`SELECT (v).* FROM holder ORDER BY k`, []string{"id", "note", "extra"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(7), "x", nil}, {nil, nil, nil}, {nil, nil, nil}}))
				run(fixture.Exec(`INSERT INTO holder VALUES (4,$1)`, 1, "(8,y,z)"))
				run(fixture.Exec(`ALTER TYPE pair RENAME ATTRIBUTE note TO label`, 0))
				check([][]any{{"(7,x,)"}, {nil}, {"(,,)"}, {"(8,y,z)"}})
				run(fixture.Select(`SELECT (v).* FROM holder WHERE k=1`, []string{"id", "label", "extra"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(7), "x", nil}}))
				run(fixture.Exec(`ALTER TYPE pair DROP ATTRIBUTE label`, 0))
				check([][]any{{"(7,)"}, {nil}, {"(,)"}, {"(8,z)"}})
				run(fixture.Select(`SELECT (v).* FROM holder WHERE k=4`, []string{"id", "extra"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "z"}}))
				run(fixture.Exec(`ALTER TYPE pair DROP ATTRIBUTE extra, ADD ATTRIBUTE extra text`, 0))
				check([][]any{{"(7,)"}, {nil}, {"(,)"}, {"(8,)"}})
				run(fixture.Exec(`ALTER TYPE pair DROP ATTRIBUTE id, DROP ATTRIBUTE extra`, 0))
				check([][]any{{"()"}, {nil}, {"()"}, {"()"}})
			})
		})
	}
}

func TestReferenceCompositeAlterArrays(t *testing.T) {
	for _, container := range []string{"array", "domain_array", "element_domain", "nested"} {
		t.Run(container, func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`)
				typ := "pair[]"
				switch container {
				case "domain_array":
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped AS pair[]`)
					typ = "wrapped"
				case "element_domain":
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped AS pair`)
					typ = "wrapped[]"
				case "nested":
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE wrapped AS (a pair[])`)
					typ = "wrapped"
				}
				fixture.CompositeReferenceSetup(t, p, b, c, "CREATE TABLE holder(k integer,v "+typ+")")
				nativeType := "_PAIR"
				if container == "element_domain" {
					nativeType = "_WRAPPED"
				} else if container == "nested" {
					nativeType = ""
				}
				wrap := func(s string) string {
					if container != "nested" {
						return s
					}
					// Empty arrays have no record-output quoting characters.
					if s == "{}" {
						return "({})"
					}
					// Composite output quotes the array's quotes a second time.
					r := ""
					for _, ch := range s {
						if ch == '"' || ch == '\\' {
							r += string(ch)
						}
						r += string(ch)
					}
					return "(\"" + r + "\")"
				}
				inputs := []string{`{"(7,x)",NULL,"(9,y)"}`, `{}`, `[0:2]={"(7,x)",NULL,"(9,y)"}`, `{{"(7,x)"},{"(9,y)"}}`}
				for i, v := range inputs {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO holder VALUES ($1,$2)`, 1, i+1, wrap(v))))
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (5,NULL)`, 1))
				check := func(values []string) {
					w := [][]any{}
					for _, v := range values {
						w = append(w, []any{wrap(v)})
					}
					w = append(w, []any{nil})
					for _, prepared := range []bool{false, true} {
						fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, w, prepared, nativeType)
					}
				}
				check(inputs)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER TYPE pair ADD ATTRIBUTE extra text`, 0))
				added := []string{`{"(7,x,)",NULL,"(9,y,)"}`, `{}`, `[0:2]={"(7,x,)",NULL,"(9,y,)"}`, `{{"(7,x,)"},{"(9,y,)"}}`}
				check(added)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`ALTER TYPE pair RENAME ATTRIBUTE note TO label`, 0)))
				check(added)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER TYPE pair DROP ATTRIBUTE label`, 0))
				check([]string{`{"(7,)",NULL,"(9,)"}`, `{}`, `[0:2]={"(7,)",NULL,"(9,)"}`, `{{"(7,)"},{"(9,)"}}`})
			})
		})
	}
	t.Run("escaping_and_late_input_failure", func(t *testing.T) {
		fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE TABLE holder(k integer,v pair[])`)
			input := `{"(7,\"a,b\")","(8,\"\")","(9,\"a\\\\b\")","(10,\"a\\\"b\")",NULL}`
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO holder VALUES(1,$1)`, 1, input)))
			canonical := `{"(7,\"a,b\")","(8,\"\")","(9,\"a\\\\b\")","(10,\"a\"\"b\")",NULL}`
			fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{canonical}}, true, "_PAIR")
			fixture.CompositeReferenceSetup(t, p, b, c, `ALTER TYPE pair ADD ATTRIBUTE extra text`)
			want := `{"(7,\"a,b\",)","(8,\"\",)","(9,\"a\\\\b\",)","(10,\"a\"\"b\",)",NULL}`
			fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{want}}, false, "_PAIR")
			d := fixture.CompositeReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
			d.Context = "unnamed portal parameter $1 = '...'"
			fixture.CompositeReferenceError(t, p, c, `INSERT INTO holder VALUES(2,$1)`, "execute", d, `{"(1,ok,)","(bad,late,)"}`)
			fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{want}}, true, "_PAIR")
			fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE empty_pair AS ()`, `CREATE TABLE empty_holder(v empty_pair[])`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO empty_holder VALUES($1)`, 1, `{(),NULL}`))
			fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM empty_holder`, [][]any{{`{(),NULL}`}}, true, "_EMPTY_PAIR")
		})
	})
}

func TestReferenceCompositeAlterDescriptor(t *testing.T) {
	t.Run("storage_free", func(t *testing.T) {
		fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id varchar(5),note text COLLATE "C")`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`ALTER TYPE pair ALTER ATTRIBUTE id SET DATA TYPE integer`, 0)))
			step := fixture.Prepared(fixture.Select(`SELECT '(7,x)'::pair AS v`, []string{"v"}, []string{""}, [][]any{{"(7,x)"}}))
			step.CheckMetadata = fixture.EnumReferenceMetadata(b.PG, false)
			fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.CompositeReferenceSetup(t, p, b, c, `ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint`, `ALTER TYPE pair ALTER ATTRIBUTE note TYPE text`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT attname,atttypid::regtype::text AS typ,attcollation::bigint AS coll FROM pg_attribute WHERE attrelid='pair'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum`, []string{"attname", "typ", "coll"}, []string{"NAME", "TEXT", "INT8"}, [][]any{{"id", "bigint", int64(0)}, {"note", "text", int64(100)}}))
			fixture.CompositeReferenceSetup(t, p, b, c, `ALTER TYPE pair ALTER ATTRIBUTE note TYPE text COLLATE pg_catalog."C"`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT attcollation::bigint AS coll FROM pg_attribute WHERE attrelid='pair'::regclass::oid AND attname='note'`, []string{"coll"}, []string{"INT8"}, [][]any{{int64(950)}}))
			fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE identity_first AS (id integer,note text)`, `ALTER TYPE identity_first ALTER ATTRIBUTE id TYPE integer, ALTER ATTRIBUTE id TYPE bigint`, `ALTER TYPE identity_first ALTER ATTRIBUTE note TYPE text COLLATE "C", ALTER ATTRIBUTE note TYPE varchar(8)`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Select(`SELECT ('(7,x)'::identity_first).id AS id,('(7,x)'::identity_first).note AS note`, []string{"id", "note"}, []string{"INT8", "VARCHAR"}, [][]any{{int64(7), "x"}})))
		})
	})
}
