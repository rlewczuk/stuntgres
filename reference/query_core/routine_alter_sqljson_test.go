package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type routineAlterJSONCase struct {
	view, expression, canonical, typ string
	value, nullValue                 any
	unique                           bool
}

func TestReferenceRoutineAlterSQLJSONArray(t *testing.T) {
	cases := []routineAlterJSONCase{
		{"ra_json_array_null", "JSON_ARRAYAGG(v FORMAT JSON ORDER BY id NULL ON NULL RETURNING text FORMAT JSON)", "JSON_ARRAYAGG(v FORMAT JSON ORDER BY id NULL ON NULL RETURNING text)", "TEXT", "[[1], null, [2]]", nil, false},
		{"ra_json_array_absent", "JSON_ARRAYAGG(v FORMAT JSON ORDER BY id ABSENT ON NULL RETURNING bytea FORMAT JSON ENCODING UTF8)", "JSON_ARRAYAGG(v FORMAT JSON ORDER BY id RETURNING bytea)", "BYTEA", "[[1], [2]]", nil, false},
		{"ra_json_array_quoted", "JSON_ARRAYAGG(v ORDER BY id NULL ON NULL RETURNING json)", "JSON_ARRAYAGG(v ORDER BY id NULL ON NULL RETURNING json)", "JSON", `["[1]", null, "[2]"]`, nil, false},
	}
	routineAlterJSONScenario(t, false, cases, []string{"json_agg(anyelement)", "json_agg_strict(anyelement)"})
}

func TestReferenceRoutineAlterSQLJSONObject(t *testing.T) {
	cases := []routineAlterJSONCase{
		{"ra_json_object_null", "JSON_OBJECTAGG(k VALUE v FORMAT JSON NULL ON NULL WITHOUT UNIQUE KEYS RETURNING text FORMAT JSON)", "JSON_OBJECTAGG(k : v FORMAT JSON RETURNING text)", "TEXT", `{ "a" : [1] }`, `{ "a" : null }`, false},
		{"ra_json_object_absent", "JSON_OBJECTAGG(k VALUE v FORMAT JSON ABSENT ON NULL WITHOUT UNIQUE KEYS RETURNING json)", "JSON_OBJECTAGG(k : v FORMAT JSON ABSENT ON NULL RETURNING json)", "JSON", `{ "a" : [1] }`, `{  }`, false},
		{"ra_json_object_unique", "JSON_OBJECTAGG(k VALUE v FORMAT JSON NULL ON NULL WITH UNIQUE KEYS RETURNING text)", "JSON_OBJECTAGG(k : v FORMAT JSON WITH UNIQUE KEYS RETURNING text)", "TEXT", `{ "a" : [1] }`, `{ "a" : null }`, true},
		{"ra_json_object_unique_absent", "JSON_OBJECTAGG(k VALUE v FORMAT JSON ABSENT ON NULL WITH UNIQUE KEYS RETURNING json)", "JSON_OBJECTAGG(k : v FORMAT JSON ABSENT ON NULL WITH UNIQUE KEYS RETURNING json)", "JSON", `{ "a" : [1] }`, `{  }`, true},
	}
	routineAlterJSONScenario(t, true, cases, []string{`json_object_agg("any","any")`, `json_object_agg_strict("any","any")`, `json_object_agg_unique("any","any")`, `json_object_agg_unique_strict("any","any")`})
}

func routineAlterJSONScenario(t *testing.T, object bool, cases []routineAlterJSONCase, signatures []string) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "ra_json_impl")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, signature := range signatures {
				restore := routineAlterBuiltinCleanup(t, p, c, "pg_catalog."+signature)
				defer restore()
			}
			routineExec(t, p, b, c, "CREATE SCHEMA ra_json_impl", "CREATE TABLE ra_json_input(id integer,k text,v text)")
			input := "INSERT INTO ra_json_input VALUES(1,'a','[1]'),(2,'b',NULL),(3,'c','[2]')"
			affected := int64(3)
			if object {
				input, affected = "INSERT INTO ra_json_input VALUES(1,'a','[1]')", 1
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(input, affected))
			for _, tc := range cases {
				routineExec(t, p, b, c, "CREATE VIEW "+tc.view+" AS SELECT "+tc.expression+" AS v FROM ra_json_input")
			}
			canonical := func(conn *sql.Conn, backend fixture.Backend, phase string, replayed bool) {
				t.Helper()
				from := "ra_json_input"
				if replayed {
					// Actual emitted-stream PostgreSQL18.6 oracle (report60):
					// the hygiene alias survives, redundant column aliases do not.
					from = "ra_json_input __view_source_0"
				}
				for _, tc := range cases {
					t.Run(phase+"/"+tc.view+"/canonical", func(t *testing.T) {
						want := " SELECT " + tc.canonical + " AS v\n   FROM " + from + ";"
						routineAlterRows(t, p, backend, conn, "SELECT pg_get_viewdef('"+tc.view+"'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{want}})
					})
				}
			}
			canonical(c, b, "before ALTER", false)
			for _, signature := range signatures {
				open := strings.IndexByte(signature, '(')
				name, args := signature[:open], signature[open:]
				oid := routineAlterOID(t, p, c, name)
				// Exercise both rename and relocation on the actual selected
				// implementation, only AFTER every consumer has been frozen.
				routineExec(t, p, b, c, "ALTER FUNCTION pg_catalog."+signature+" RENAME TO ra_json_"+name,
					"ALTER FUNCTION pg_catalog.ra_json_"+name+args+" SET SCHEMA ra_json_impl")
				routineAlterSameOID(t, p, c, "ra_json_"+name, oid)
				// Do not recreate the vacated label: an alias would conceal
				// loss of the original checked callable identity.
				routineAlterRows(t, p, b, c, "SELECT proname FROM pg_proc WHERE pronamespace='ra_json_impl'::regnamespace AND proname='ra_json_"+name+"'", []string{"proname"}, []string{"NAME"}, [][]any{{"ra_json_" + name}})
			}
			check := func(conn *sql.Conn, backend fixture.Backend, phase string, replayed bool) {
				t.Helper()
				canonical(conn, backend, phase, replayed)
				for _, signature := range signatures {
					name := signature[:strings.IndexByte(signature, '(')]
					arguments, want := "1", "[1]"
					if object {
						arguments, want = "'a'::text,1", `{ "a" : 1 }`
					}
					routineAlterRows(t, p, backend, conn, "SELECT ra_json_impl.ra_json_"+name+"("+arguments+") AS v", []string{"v"}, []string{"JSON"}, [][]any{{want}})
				}
				for _, tc := range cases {
					routineAlterRows(t, p, backend, conn, "SELECT v FROM "+tc.view, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}})
				}
				if object {
					routineAlterJSONObjectBoundaries(t, p, backend, conn, cases)
				}
			}
			check(c, b, "after ALTER", false)
			if !b.PG {
				routineAlterJSONReplay(t, p, b, c, signatures, check)
			}
		})
	}
}

func routineAlterJSONObjectBoundaries(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, cases []routineAlterJSONCase) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE ra_json_input SET v=NULL", 1))
	for _, tc := range cases {
		routineAlterRows(t, p, b, c, "SELECT v FROM "+tc.view, []string{"v"}, []string{tc.typ}, [][]any{{tc.nullValue}})
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_json_input VALUES(2,'a',NULL)", 1))
	for _, tc := range cases {
		if tc.unique {
			d := fixture.ControlsReferenceDiagnostic("22030", `duplicate JSON object key value: "a"`)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT v FROM "+tc.view, d)
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		} else {
			want := `{ "a" : null, "a" : null }`
			if strings.Contains(tc.view, "absent") {
				want = `{  }`
			}
			routineAlterRows(t, p, b, c, "SELECT v FROM "+tc.view, []string{"v"}, []string{tc.typ}, [][]any{{want}})
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ra_json_input WHERE id=2", 1))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE ra_json_input SET v='[1]'", 1))
}

func routineAlterJSONReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, signatures []string, check func(*sql.Conn, fixture.Backend, string, bool)) {
	t.Helper()
	for round := range 2 {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b, fmt.Sprintf("native replay %d", round+1), true)
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/json-fresh")
		if err != nil {
			t.Fatal(err)
		}
		func() {
			control := sql.OpenDB(connector)
			defer control.Close()
			fresh := sql.OpenDB(fixture.MigrationRecordedConnector(t, connector))
			defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx, control, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			conn, err := fresh.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			check(conn, fixture.Backend{Recorder: &fixture.Recorder{}}, fmt.Sprintf("fresh replay %d", round+1), true)
		}()
		if p.Postgres == nil {
			continue
		}
		func() {
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pg.Close()
			// Register restoration before executing ANY emitted SQL so a
			// partial replay cannot strand renamed system aggregates.
			defer func() {
				if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS ra_json_impl CASCADE; DROP TABLE IF EXISTS ra_json_input CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			for _, signature := range signatures {
				restore := routineAlterBuiltinCleanup(t, p, pg, "pg_catalog."+signature)
				defer restore()
			}
			if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual emitted SQL/JSON stream replay", err)
			}
			check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, fmt.Sprintf("PostgreSQL emitted replay %d", round+1), true)
		}()
	}
}
