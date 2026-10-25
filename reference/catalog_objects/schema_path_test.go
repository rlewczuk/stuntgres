package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSchemaPathsPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "sp1", "sp2", "sp_shape", "sp_renamed", "path.dot", "path,comma", "path\"quote")
	for _, q := range []string{"CREATE SCHEMA sp1 CREATE TABLE t(id INT)", "CREATE SCHEMA sp2 CREATE TABLE t(id INT)", "CREATE SCHEMA sp_shape CREATE TABLE t(id TEXT)", `CREATE SCHEMA "path.dot"`, `CREATE SCHEMA "path,comma"`, `CREATE SCHEMA "path""quote"`} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO sp1.t VALUES(7)", 1))
	p.Step(t, fixture.Exec("INSERT INTO sp2.t VALUES(9)", 1))
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
			run(a, fixture.Exec("SET search_path TO sp1,sp2", 0))
			run(b, fixture.Exec("SET search_path TO sp2,sp1", 0))
			held, err := a.PrepareContext(p.Ctx, "SELECT id FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			heldInsert, err := a.PrepareContext(p.Ctx, "INSERT INTO t VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer heldInsert.Close()
			check := func(want int64) {
				r, err := held.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				fixture.SchemaReferenceRow(t, r, "id", "INT4", want)
			}
			check(7)
			run(b, fixture.Prepared(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})))
			for _, form := range []string{"SELECT current_schema", "SELECT current_schema()"} {
				run(a, fixture.Prepared(fixture.Select(form, []string{"current_schema"}, []string{"NAME"}, [][]any{{"sp1"}})))
			}
			run(a, fixture.Prepared(fixture.Exec("SELECT current_schema()", 1)))
			run(a, fixture.Exec("SET search_path TO missing,sp2,sp2,sp1", 0))
			check(9)
			result, err := heldInsert.ExecContext(p.Ctx, int64(12))
			if err != nil {
				t.Fatal(err)
			}
			count, err := result.RowsAffected()
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
			run(a, fixture.Select("SELECT id FROM sp2.t WHERE id=12", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}}))
			run(a, fixture.Select("SELECT id FROM sp1.t WHERE id=12", []string{"id"}, []string{"INT4"}, nil))
			run(a, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"missing, sp2, sp2, sp1"}}))
			run(b, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"sp2, sp1"}}))
			run(a, fixture.Exec("SET search_path TO ''", 0))
			run(a, fixture.Select("SELECT current_schema()", []string{"current_schema"}, []string{"NAME"}, [][]any{{nil}}))
			run(a, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{`""`}}))
			run(a, fixture.Exec("SET search_path TO 'sp1,'", 0))
			run(a, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{`"sp1,"`}}))
			run(a, fixture.Select("SELECT current_schema", []string{"current_schema"}, []string{"NAME"}, [][]any{{nil}}))
			run(a, fixture.Select("SELECT set_config('search_path','',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{""}}))
			run(a, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{""}}))
			run(a, fixture.Exec(`SET search_path TO "sp1","path.dot",public`, 0))
			run(a, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{`sp1, "path.dot", public`}}))
			run(a, fixture.Exec("SET search_path TO ''", 0))
			run(a, fixture.SchemaReferenceCreationError("CREATE TABLE no_destination(id INT)", "3F000", "no schema has been selected to create in", false, "14"))
			run(a, fixture.Select("SELECT id FROM sp1.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(a, fixture.Exec("RESET search_path", 0))
			run(a, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{`"$user", public`}}))
			for _, tc := range []struct{ sql, name string }{{`"path.dot"`, "path.dot"}, {`"path,comma"`, "path,comma"}, {`"path""quote"`, "path\"quote"}} {
				run(a, fixture.Exec("SET search_path TO "+tc.sql, 0))
				run(a, fixture.Select("SELECT current_schema", []string{"current_schema"}, []string{"NAME"}, [][]any{{tc.name}}))
			}
			run(a, fixture.Exec("SET search_path TO sp2", 0))
			for _, prepared := range []bool{false, true} {
				run(a, fixture.SchemaReferenceError("SELECT set_config('search_path','sp1,',false)", "22023", `invalid value for parameter "search_path": "sp1,"`, "List syntax is invalid.", prepared, "execute", ""))
			}
			run(a, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"sp2"}}))
			// Both backends retain the original INT4 input contract and
			// assignment-coerce to TEXT after path reanalysis.
			run(a, fixture.Exec("SET search_path TO sp_shape", 0))
			result, err = heldInsert.ExecContext(p.Ctx, int64(9))
			if err != nil {
				t.Fatal("held INT4 INSERT into TEXT", err)
			}
			count, err = result.RowsAffected()
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
			run(a, fixture.Select("SELECT id FROM sp_shape.t", []string{"id"}, []string{"TEXT"}, [][]any{{"9"}}))
			run(a, fixture.Exec("SET search_path TO sp_shape", 0))
			_, err = held.QueryContext(p.Ctx)
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"})
			run(a, fixture.Exec("SET search_path TO sp1", 0))
			retained, err := held.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			run(b, fixture.Exec("ALTER SCHEMA sp1 RENAME TO sp_renamed", 0))
			fixture.SchemaReferenceRow(t, retained, "id", "INT4", int64(7))
			run(a, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"sp1"}}))
			for _, prepared := range []bool{false, true} {
				s := fixture.SchemaReferenceError("SELECT id FROM sp1.t", "42P01", `relation "sp1.t" does not exist`, "", prepared, "prepare", "16")
				s.SelectRows = true
				run(a, s)
			}
			run(a, fixture.Exec("ALTER SCHEMA sp_renamed RENAME TO sp1", 0))
		})
	}
}

func TestReferenceSchemaSearchPath(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "sp1", "sp2", "sp1,")
	for _, q := range []string{"CREATE SCHEMA sp1 CREATE TABLE t(id INT)", "CREATE SCHEMA sp2 CREATE TABLE t(id INT)"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO sp1.t VALUES(7)", 1))
	p.Step(t, fixture.Exec("INSERT INTO sp2.t VALUES(9)", 1))
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
			run(b, fixture.Exec("SET search_path TO sp1", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("SET search_path TO 'sp1,'", 0)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(a, s)
				run(a, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{`"sp1,"`}}))
				run(a, fixture.Select("SELECT current_schema", []string{"current_schema"}, []string{"NAME"}, [][]any{{nil}}))
			}
			run(a, fixture.Exec(`CREATE SCHEMA "sp1," CREATE TABLE t(id INT)`, 0))
			run(a, fixture.Exec(`INSERT INTO "sp1,".t VALUES(11)`, 1))
			run(a, fixture.Exec("SET search_path TO sp2", 0))
			held, err := a.PrepareContext(p.Ctx, "SELECT id FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared_%v", prepared), func(t *testing.T) {
					step := func(s fixture.Step) {
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, a, backend.Recorder, s)
					}
					text := func(q, label, value string) {
						step(fixture.Select(q, []string{label}, []string{"TEXT"}, [][]any{{value}}))
					}
					stored := func(value string) {
						text("SHOW search_path", "search_path", value)
						text("SELECT current_setting('search_path')", "current_setting", value)
					}
					current := func(value any) {
						step(fixture.Select("SELECT current_schema()", []string{"current_schema"}, []string{"NAME"}, [][]any{{value}}))
					}
					step(fixture.Exec("SET search_path TO 'sp1,'", 0))
					stored(`"sp1,"`)
					current("sp1,")
					step(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
					step(fixture.Select("SELECT id FROM sp1.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					for _, q := range []string{"SET search_path TO ''", "SELECT set_config('search_path','',false)"} {
						value := `""`
						if strings.HasPrefix(q, "SET") {
							step(fixture.Exec(q, 0))
						} else {
							value = ""
							text(q, "set_config", value)
						}
						stored(value)
						current(nil)
						step(fixture.SchemaReferenceCreationError("CREATE TABLE no_destination(id INT)", "3F000", "no schema has been selected to create in", false, "14"))
						step(fixture.Select("SELECT id FROM sp1.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
					step(fixture.Exec(`SET search_path TO "lowercase","A.B",public`, 0))
					stored(`lowercase, "A.B", public`)
					current("public")
					for _, raw := range []string{`""`, `"", sp2`} {
						text("SELECT set_config('search_path','"+raw+"',false)", "set_config", raw)
						stored(raw)
						if raw == `""` {
							current(nil)
						} else {
							current("sp2")
							step(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
						}
					}
					step(fixture.Exec("SET search_path TO '',sp2", 0))
					stored(`"", sp2`)
					current("sp2")
					step(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
					step(fixture.Exec("SET search_path TO sp2", 0))
					bad := fixture.SchemaReferenceError("SELECT set_config('search_path','sp1,',false)", "22023", `invalid value for parameter "search_path": "sp1,"`, "List syntax is invalid.", false, "execute", "")
					bad.SelectRows = true
					step(bad)
					stored("sp2")
					current("sp2")
					r, err := held.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					fixture.SchemaReferenceRow(t, r, "id", "INT4", int64(9))
					run(b, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"sp1"}}))
					run(b, fixture.Prepared(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})))
				})
			}
		})
	}
}

func TestReferenceSchemaUserExpansion(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"spath_a", "spath_b"})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "spath_a", "spath_b")
	for _, q := range []string{"CREATE USER spath_a", "CREATE USER spath_b", "CREATE SCHEMA spath_a AUTHORIZATION spath_a CREATE TABLE t(id INT)", "CREATE SCHEMA spath_b AUTHORIZATION spath_b CREATE TABLE t(id INT)"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO spath_a.t VALUES(7)", 1))
	p.Step(t, fixture.Exec("INSERT INTO spath_b.t VALUES(9)", 1))
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
			run(b, fixture.Exec("SET SESSION AUTHORIZATION spath_b", 0))
			run(a, fixture.Exec("SET SESSION AUTHORIZATION spath_a", 0))
			held, err := a.PrepareContext(p.Ctx, "SELECT id FROM t")
			if err != nil {
				t.Fatal("initial Prepare", err)
			}
			defer held.Close()
			for _, tc := range []struct {
				role  string
				value int64
			}{{"spath_a", 7}, {"spath_b", 9}} {
				run(a, fixture.Prepared(fixture.Exec("SET SESSION AUTHORIZATION "+tc.role, 0)))
				run(a, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{`"$user", public`}}))
				run(a, fixture.Prepared(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{tc.role}})))
				run(a, fixture.Prepared(fixture.Select("SELECT current_schema()", []string{"current_schema"}, []string{"NAME"}, [][]any{{tc.role}})))
				run(a, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{tc.value}}))
				run(a, fixture.Prepared(fixture.Select("SELECT id FROM "+tc.role+".t", []string{"id"}, []string{"INT4"}, [][]any{{tc.value}})))
				rows, err := held.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal("held Execute", err)
				}
				fixture.SchemaReferenceRow(t, rows, "id", "INT4", tc.value)
				run(b, fixture.Prepared(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})))
			}
			run(a, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			authenticator := "postgres"
			if backend.PG {
				authenticator = "stuntgres"
			}
			run(a, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{authenticator}}))
			run(a, fixture.Select("SELECT current_schema()", []string{"current_schema"}, []string{"NAME"}, [][]any{{"public"}}))
			run(b, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	}
}
