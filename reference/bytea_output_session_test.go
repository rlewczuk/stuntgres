package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"stuntgres"
	"testing"

	"github.com/ngrok/sqlmw"
)

func TestReferenceByteaOutputLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			db, c, record := noticeReferencePool(t, p, b.pg, &notices)
			db.SetMaxOpenConns(2)
			db.SetMaxIdleConns(2)
			peer, err := db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, record, s) }
			run(referenceExec("CREATE TABLE bytea_output_values(id INTEGER,v BYTEA)", 0))
			run(referenceExec(`INSERT INTO bytea_output_values VALUES(1,'\x0080ff')`, 1))
			witness := func(conn *sql.Conn, mode string, prepared bool) {
				t.Helper()
				text := `\x0080ff`
				if mode == "escape" {
					text = `\000\200\377`
				}
				for _, s := range []referenceStep{
					referenceSelect("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{mode}}),
					referenceSelect("SELECT current_setting('bytea_output')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{mode}}),
					referenceSelect("SELECT v::text AS rendered FROM bytea_output_values WHERE id=1", []string{"rendered"}, []string{"TEXT"}, [][]any{{text}}),
				} {
					s.prepared = prepared
					referenceRunStep(t, p.ctx, conn, record, s)
				}
			}
			witness(c, "hex", false)
			witness(peer, "hex", false)
			setQuery := "SET bytea_output=escape"
			set, err := c.PrepareContext(p.ctx, setQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			witness(c, "hex", false) // Prepare must not publish the setting.
			referenceRunStep(t, p.ctx, referenceHeldStatement{c, set, setQuery}, record, referenceExec(setQuery, 0))
			witness(c, "escape", false)
			witness(peer, "hex", false)
			for _, prepared := range []bool{false, true} {
				exec := func(q string) { s := referenceExec(q, 0); s.prepared = prepared; run(s) }
				for _, reset := range []string{"SET bytea_output=DEFAULT", "RESET bytea_output", "RESET ALL"} {
					exec("SET bytea_output=escape")
					witness(c, "escape", prepared)
					exec(reset)
					witness(c, "hex", prepared)
				}
				s := referenceSelect("SELECT set_config('bytea_output','ESCAPE',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"escape"}})
				s.prepared = prepared
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
					s := referenceImplicitError("SET bytea_output='"+invalid+"'", referenceDiagnostic{Code: "22023", Message: `invalid value for parameter "bytea_output": "` + invalid + `"`, Hint: "Available values: escape, hex."})
					s.errorStage = "execute"
					s.prepared = prepared
					run(s)
					witness(c, "escape", prepared)
				}
				exec("RESET bytea_output")
				exec("BEGIN")
				exec("SET bytea_output=escape")
				bad := referenceImplicitError("SET bytea_output=bogus", referenceDiagnostic{Code: "22023", Message: `invalid value for parameter "bytea_output": "bogus"`, Hint: "Available values: escape, hex."})
				bad.errorStage = "execute"
				bad.prepared = prepared
				run(bad)
				exec("ROLLBACK")
				witness(c, "hex", prepared)
				before := len(notices)
				if prepared {
					q := "SET LOCAL bytea_output=escape"
					stmt, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					if len(notices) != before {
						t.Fatal("Prepare emitted LOCAL warning", notices)
					}
					witness(c, "hex", false)
					referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, record, referenceExec(q, 0))
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				} else {
					exec("SET LOCAL bytea_output=escape")
				}
				want := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
				if len(notices) != before+1 || notices[before] != want {
					t.Fatalf("LOCAL warning got %#v want %#v", notices[before:], want)
				}
				witness(c, "hex", prepared)
			}
			run(referenceExec("SET bytea_output=escape", 0))
			if err := set.Close(); err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			// The peer occupies the other physical connection: only A is idle.
			c, err = db.Conn(p.ctx)
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
			fresh, err := db.Conn(p.ctx)
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
	roleReferenceCleanup(t, []string{role})
	p := newReferenceScenario(t)
	dsn := "reference/" + t.Name()
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE USER "+role, 0))
			var db *sql.DB
			if b.pg {
				if _, err := referenceAdmin.ExecContext(p.ctx, "ALTER ROLE "+role+" PASSWORD 'role-core-fixture'"); err != nil {
					t.Fatal(err)
				}
				u, err := url.Parse(referenceDSN)
				if err != nil {
					t.Fatal(err)
				}
				u.User = url.UserPassword(role, "role-core-fixture")
				db, err = sql.Open("stuntgres_reference_postgres", u.String())
				if err != nil {
					t.Fatal(err)
				}
			} else {
				connector, err := p.nativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, role)
				if err != nil {
					t.Fatal(err)
				}
				wrapped := sqlmw.Driver(noticeReferenceDriver{connector}, referenceInterceptor{})
				configured, err := wrapped.(driver.DriverContext).OpenConnector("")
				if err != nil {
					t.Fatal(err)
				}
				db = sql.OpenDB(configured)
			}
			defer db.Close()
			c, err := db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
			run(referenceSelect("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{role, role}}))
			for _, prepared := range []bool{false, true} {
				witness := func(mode, text string) {
					for _, s := range []referenceStep{
						referenceSelect("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{mode}}),
						referenceSelect("SELECT $1::bytea::text AS rendered", []string{"rendered"}, []string{"TEXT"}, [][]any{{text}}, []byte{0, 128, 255}),
					} {
						s.prepared = prepared
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
					s := referenceExec(step.q, 0)
					s.prepared = prepared
					run(s)
					witness(step.mode, step.text)
				}
			}
		})
	}
}
