package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSearchPathCurrentSchemas(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "sp1", "sp2", "path,comma", "path{brace}", "NULL", "path space", "path\"quote", "path\\slash")
	for _, q := range []string{"CREATE SCHEMA sp1", "CREATE SCHEMA sp2", `CREATE SCHEMA "path,comma"`, `CREATE SCHEMA "path{brace}"`, `CREATE SCHEMA "NULL"`, `CREATE SCHEMA "path space"`, `CREATE SCHEMA "path""quote"`, `CREATE SCHEMA "path\slash"`} {
		p.Step(t, fixture.Exec(q, 0))
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			run := func(c *sql.Conn, s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			run(a, fixture.Exec("SET search_path TO missing,sp2,sp2,sp1", 0))
			run(b, fixture.Exec("SET search_path TO sp1", 0))
			held, err := a.PrepareContext(p.Ctx, "SELECT current_schemas($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			check := func(flag any, want any) {
				t.Helper()
				rows, err := held.QueryContext(p.Ctx, flag)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 {
					t.Fatal(types, err)
				}
				scan := reflect.TypeOf("")
				if backend.PG {
					scan = reflect.TypeOf((*any)(nil)).Elem()
				}
				if types[0].Name() != "current_schemas" || types[0].DatabaseTypeName() != "_NAME" || types[0].ScanType() != scan {
					t.Fatalf("metadata %s %s %v want %v", types[0].Name(), types[0].DatabaseTypeName(), types[0].ScanType(), scan)
				}
				if nullable, known := types[0].Nullable(); (!backend.PG && (!nullable || !known)) || (backend.PG && (nullable || known)) {
					t.Fatal("array nullable", nullable, known)
				}
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				var raw any
				if err := rows.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var got any = raw
				if want != nil && backend.PG {
					bytes, ok := raw.([]byte)
					if !ok {
						t.Fatalf("pq array export %T", raw)
					}
					got = string(bytes)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("value %#v want %#v", got, want)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
			}
			run(a, fixture.Prepared(fixture.Exec("SELECT current_schemas(false)", 1)))
			check(false, "{sp2,sp1}")
			check(true, "{pg_catalog,sp2,sp1}")
			check(nil, nil)
			run(a, fixture.Exec("SET search_path TO missing,sp1,sp1,sp2", 0))
			check(false, "{sp1,sp2}")
			check(true, "{pg_catalog,sp1,sp2}")
			check(nil, nil)
			var retained *sql.Rows
			if !backend.PG {
				retained, err = held.QueryContext(p.Ctx, false)
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
			}
			run(a, fixture.Exec("SET search_path TO sp2,sp1", 0))
			if retained != nil {
				fixture.ArrayReferenceConsumeHeld(t, retained, [][]any{{"{sp1,sp2}"}}, []string{"_NAME"})
			}
			check(false, "{sp2,sp1}")
			run(a, fixture.Select("SELECT pg_catalog.current_schemas(false)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{sp2,sp1}"}}))
			run(a, fixture.Prepared(fixture.Select("SELECT pg_catalog.current_schemas(false)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{sp2,sp1}"}})))
			run(a, fixture.Exec("SET search_path TO sp2,pg_catalog,sp1", 0))
			check(false, "{sp2,pg_catalog,sp1}")
			check(true, "{sp2,pg_catalog,sp1}")
			run(a, fixture.Exec("SET search_path TO sp1,pg_catalog,sp2", 0))
			check(false, "{sp1,pg_catalog,sp2}")
			check(true, "{sp1,pg_catalog,sp2}")
			run(a, fixture.Exec("SELECT set_config('search_path','',false)", 1))
			check(false, "{}")
			check(true, "{pg_catalog}")
			run(b, fixture.Select("SELECT current_schemas(false)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{sp1}"}}))
			run(b, fixture.Exec("SET search_path TO sp1,sp2", 0))
			run(b, fixture.Select("SELECT current_schemas(false)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{sp1,sp2}"}}))
			run(b, fixture.Exec("SET search_path TO sp1", 0))
			run(a, fixture.Exec(`SET search_path TO "path,comma","path{brace}","NULL","path space","path""quote","path\slash"`, 0))
			check(false, `{"path,comma","path{brace}","NULL","path space","path\"quote","path\\slash"}`)
			run(a, fixture.Exec("SET search_path TO sp1", 0))
			run(a, fixture.Exec("SET search_path TO pg_temp,sp1", 0))
			pendingTemp := ""
			if backend.PG {
				// Match the existing PendingTempBoundary witness: PostgreSQL can
				// materialize pg_temp on this route before a TEMP object is created.
				// Derive its name independently, not from current_schemas output.
				if err := a.QueryRowContext(p.Ctx, "SELECT current_schema()").Scan(&pendingTemp); err != nil || !strings.HasPrefix(pendingTemp, "pg_temp_") {
					t.Fatal(pendingTemp, err)
				}
				check(false, "{"+pendingTemp+",sp1}")
				check(true, "{pg_catalog,"+pendingTemp+",sp1}")
			} else {
				check(false, "{sp1}")
				check(true, "{pg_catalog,sp1}")
			}
			run(a, fixture.Exec("CREATE TEMP SEQUENCE search_path_temp", 0))
			var temp string
			run(a, fixture.Exec("SET search_path TO pg_temp,sp1", 0))
			if err := a.QueryRowContext(p.Ctx, "SELECT current_schema()").Scan(&temp); err != nil || !strings.HasPrefix(temp, "pg_temp_") {
				t.Fatal(temp, err)
			}
			if pendingTemp != "" && temp != pendingTemp {
				t.Fatal("TEMP sequence changed physical-session namespace", pendingTemp, temp)
			}
			run(a, fixture.Exec("SET search_path TO sp1", 0))
			check(false, "{sp1}")
			check(true, "{"+temp+",pg_catalog,sp1}")
			run(a, fixture.Exec("SET search_path TO sp1,pg_temp,pg_temp", 0))
			check(false, "{sp1,"+temp+"}")
			check(true, "{pg_catalog,sp1,"+temp+"}")
			run(a, fixture.Exec("SET search_path TO sp2", 0))
			want := fixture.ControlsReferenceDiagnostic("22023", `invalid value for parameter "search_path": "sp1,"`)
			want.Detail = "List syntax is invalid."
			run(a, fixture.ErrorDiagnostic("SELECT set_config('search_path','sp1,',false)", want))
			check(false, "{sp2}")
			run(a, fixture.Select("SELECT num_nulls(NULL)", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}
func TestReferenceSearchPathStoredViewRuntime(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "sp1", "sp2")
	for _, q := range []string{"CREATE SCHEMA sp1", "CREATE SCHEMA sp2", "CREATE TABLE sp1.t(id INT)", "CREATE VIEW sp1.runtime_paths AS SELECT pg_catalog.current_schemas(false) AS paths FROM sp1.t"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO sp1.t VALUES(7)", 1))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			run := func(c *sql.Conn, step fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, step) }
			run(a, fixture.Exec("SET search_path TO sp1", 0))
			run(b, fixture.Exec("SET search_path TO sp1", 0))
			const query = "SELECT paths FROM sp1.runtime_paths"
			held, err := a.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			peer, err := b.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			checkHeld := func(stmt *sql.Stmt, want string) {
				t.Helper()
				rows, err := stmt.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				scan := reflect.TypeOf("")
				if backend.PG {
					scan = reflect.TypeOf((*any)(nil)).Elem()
				}
				if err != nil || len(types) != 1 || types[0].ScanType() != scan {
					rows.Close()
					t.Fatal("stored view scan metadata", types, err)
				}
				fixture.SchemaReferenceRow(t, rows, "paths", "_NAME", want)
			}
			direct := func(c *sql.Conn, want string) {
				t.Helper()
				run(c, fixture.Select(query, []string{"paths"}, []string{"_NAME"}, [][]any{{want}}))
			}
			direct(a, "{sp1}")
			checkHeld(held, "{sp1}")
			direct(b, "{sp1}")
			checkHeld(peer, "{sp1}")
			run(a, fixture.Exec("SET search_path TO sp2", 0))
			direct(a, "{sp2}")
			checkHeld(held, "{sp2}")
			direct(b, "{sp1}")
			checkHeld(peer, "{sp1}")
			run(a, fixture.Exec("SELECT set_config('search_path','',false)", 1))
			direct(a, "{}")
			checkHeld(held, "{}")
			direct(b, "{sp1}")
			checkHeld(peer, "{sp1}")
			if !backend.PG {
				run(a, fixture.Exec("SET search_path TO sp2,sp1", 0))
				check := fixture.Select(query, []string{"paths"}, []string{"_NAME"}, [][]any{{"{sp2,sp1}"}})
				fixture.ArrayReferenceNativeRoundtrip(t, p, a, backend, check, fixture.Exec("SET search_path TO sp2,sp1", 0))
				checkHeld(held, "{sp2,sp1}")
				checkHeld(peer, "{sp1}")
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				target := fixture.NewFixture(t, false, true)
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET search_path TO pg_catalog", 0))
				if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				for _, q := range []string{"SELECT current_schemas(false)", query} {
					name := "current_schemas"
					if q == query {
						name = "paths"
					}
					step := fixture.Select(q, []string{name}, []string{"_NAME"}, [][]any{{"{pg_catalog}"}})
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, step)
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(step))
				}
			}
		})
	}
}
