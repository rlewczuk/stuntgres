package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/ngrok/sqlmw"
	"net/url"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceByteaOutputLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			db, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			db.SetMaxOpenConns(2)
			db.SetMaxIdleConns(2)
			peer, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, record, s) }
			run(fixture.Exec("CREATE TABLE bytea_output_values(id INTEGER,v BYTEA)", 0))
			run(fixture.Exec(`INSERT INTO bytea_output_values VALUES(1,'\x0080ff')`, 1))
			witness := func(conn *sql.Conn, mode string, prepared bool) {
				t.Helper()
				text := `\x0080ff`
				if mode == "escape" {
					text = `\000\200\377`
				}
				for _, s := range []fixture.Step{
					fixture.Select("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{mode}}),
					fixture.Select("SELECT current_setting('bytea_output')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{mode}}),
					fixture.Select("SELECT v::text AS rendered FROM bytea_output_values WHERE id=1", []string{"rendered"}, []string{"TEXT"}, [][]any{{text}}),
				} {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, record, s)
				}
			}
			witness(c, "hex", false)
			witness(peer, "hex", false)
			setQuery := "SET bytea_output=escape"
			set, err := c.PrepareContext(p.Ctx, setQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			witness(c, "hex", false) // Prepare must not publish the setting.
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, set, setQuery}, record, fixture.Exec(setQuery, 0))
			witness(c, "escape", false)
			witness(peer, "hex", false)
			for _, prepared := range []bool{false, true} {
				exec := func(q string) { s := fixture.Exec(q, 0); s.Prepared = prepared; run(s) }
				for _, reset := range []string{"SET bytea_output=DEFAULT", "RESET bytea_output", "RESET ALL"} {
					exec("SET bytea_output=escape")
					witness(c, "escape", prepared)
					exec(reset)
					witness(c, "hex", prepared)
				}
				s := fixture.Select("SELECT set_config('bytea_output','ESCAPE',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"escape"}})
				s.Prepared = prepared
				run(s)
				witness(c, "escape", prepared)
				for _, sequence := range [][]struct{ q, mode string }{
					{{"RESET bytea_output", "hex"}, {"BEGIN", "hex"}, {"SET bytea_output=escape", "escape"}, {"COMMIT", "escape"}},
					{{"RESET bytea_output", "hex"}, {"BEGIN", "hex"}, {"SET bytea_output=escape", "escape"}, {"ROLLBACK", "hex"}},
					{{"BEGIN", "hex"}, {"SET LOCAL bytea_output=escape", "escape"}, {"COMMIT", "hex"}},
					{{"BEGIN", "hex"}, {"SET LOCAL bytea_output=escape", "escape"}, {"ROLLBACK", "hex"}},
					{{"BEGIN", "hex"}, {"SET bytea_output=escape", "escape"}, {"SET LOCAL bytea_output=hex", "hex"}, {"COMMIT", "escape"}},
					{{"RESET bytea_output", "hex"}, {"BEGIN", "hex"}, {"SET bytea_output=escape", "escape"}, {"SET LOCAL bytea_output=hex", "hex"}, {"ROLLBACK", "hex"}},
					{{"BEGIN", "hex"}, {"SAVEPOINT mark", "hex"}, {"SET bytea_output=escape", "escape"}, {"ROLLBACK TO mark", "hex"}, {"COMMIT", "hex"}},
					{{"BEGIN", "hex"}, {"SAVEPOINT mark", "hex"}, {"SET LOCAL bytea_output=escape", "escape"}, {"ROLLBACK TO mark", "hex"}, {"COMMIT", "hex"}},
					{{"BEGIN", "hex"}, {"SET LOCAL bytea_output=escape", "escape"}, {"SAVEPOINT mark", "escape"}, {"SET bytea_output=hex", "hex"}, {"ROLLBACK TO mark", "escape"}, {"COMMIT", "hex"}},
				} {
					for _, transition := range sequence {
						exec(transition.q)
						witness(c, transition.mode, prepared)
						witness(peer, "hex", prepared)
					}
				}
				for _, invalid := range []string{"bogus", "he", " hex ", ""} {
					exec("SET bytea_output=escape")
					s := fixture.ImplicitError("SET bytea_output='"+invalid+"'", fixture.Diagnostic{Code: "22023", Message: `invalid value for parameter "bytea_output": "` + invalid + `"`, Hint: "Available values: escape, hex."})
					s.ErrorStage = "execute"
					s.Prepared = prepared
					run(s)
					witness(c, "escape", prepared)
				}
				exec("RESET bytea_output")
				exec("BEGIN")
				exec("SET bytea_output=escape")
				bad := fixture.ImplicitError("SET bytea_output=bogus", fixture.Diagnostic{Code: "22023", Message: `invalid value for parameter "bytea_output": "bogus"`, Hint: "Available values: escape, hex."})
				bad.ErrorStage = "execute"
				bad.Prepared = prepared
				run(bad)
				exec("ROLLBACK")
				witness(c, "hex", prepared)
				before := len(notices)
				if prepared {
					q := "SET LOCAL bytea_output=escape"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					if len(notices) != before {
						t.Fatal("Prepare emitted LOCAL warning", notices)
					}
					witness(c, "hex", false)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, record, fixture.Exec(q, 0))
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				} else {
					exec("SET LOCAL bytea_output=escape")
				}
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
				if len(notices) != before+1 || notices[before] != want {
					t.Fatalf("LOCAL warning got %#v want %#v", notices[before:], want)
				}
				witness(c, "hex", prepared)
			}
			run(fixture.Exec("SET bytea_output=escape", 0))
			if err := set.Close(); err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			// The peer occupies the other physical connection: only A is idle.
			c, err = db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			witness(c, "escape", false)
			if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			if err := c.Close(); !errors.Is(err, sql.ErrConnDone) {
				t.Fatalf("discarded Close got %v want sql.ErrConnDone", err)
			}
			fresh, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			witness(fresh, "hex", false)
			witness(peer, "hex", false)
		})
	}
}

func TestReferenceByteaOutputUserPrivilege(t *testing.T) {
	const role = "bytea_output_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	p := fixture.NewScenario(t)
	dsn := "reference/" + t.Name()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE USER "+role, 0))
			var db *sql.DB
			if b.PG {
				if _, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE "+role+" PASSWORD 'role-core-fixture'"); err != nil {
					t.Fatal(err)
				}
				u, err := url.Parse(fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
				u.User = url.UserPassword(role, "role-core-fixture")
				db, err = sql.Open("stuntgres_reference_postgres", u.String())
				if err != nil {
					t.Fatal(err)
				}
			} else {
				connector, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, role)
				if err != nil {
					t.Fatal(err)
				}
				wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{connector}, fixture.Interceptor{})
				configured, err := wrapped.(driver.DriverContext).OpenConnector("")
				if err != nil {
					t.Fatal(err)
				}
				db = sql.OpenDB(configured)
			}
			defer db.Close()
			c, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{role, role}}))
			for _, prepared := range []bool{false, true} {
				witness := func(mode, text string) {
					for _, s := range []fixture.Step{
						fixture.Select("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{mode}}),
						fixture.Select("SELECT $1::bytea::text AS rendered", []string{"rendered"}, []string{"TEXT"}, [][]any{{text}}, []byte{0, 128, 255}),
					} {
						s.Prepared = prepared
						run(s)
					}
				}
				witness("hex", `\x0080ff`)
				for _, step := range []struct{ q, mode, text string }{
					{"SET bytea_output=escape", "escape", `\000\200\377`},
					{"BEGIN", "escape", `\000\200\377`},
					{"SET LOCAL bytea_output=hex", "hex", `\x0080ff`},
					{"COMMIT", "escape", `\000\200\377`},
					{"RESET bytea_output", "hex", `\x0080ff`},
				} {
					s := fixture.Exec(step.q, 0)
					s.Prepared = prepared
					run(s)
					witness(step.mode, step.text)
				}
			}
		})
	}
}
