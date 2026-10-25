package reference_test

import (
	"database/sql"
	"io"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const loadMissing = "stuntgres_load_contract_missing"
const loadPlugin = "$libdir/plugins/" + loadMissing
const loadUnsupported = "LOAD native libraries are unsupported in embedded mode"

func loadScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			db, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.DB, b.Recorder = db, recorder
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			defer func() {
				if t.Failed() {
					c.ExecContext(p.Ctx, "ROLLBACK")
				}
			}()
			defer func() {
				if len(notices) != 0 {
					t.Errorf("unexpected notices: %#v", notices)
				}
			}()
			loadPreconditions(t, p, b, c)
			body(t, p, b, c)
		})
	}
}

func loadPreconditions(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	if !b.PG {
		return
	}
	var search string
	rows, err := c.QueryContext(p.Ctx, "SHOW dynamic_library_path")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing dynamic_library_path")
	}
	if err := rows.Scan(&search); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if search != "$libdir" {
		t.Fatalf("absence proof requires pinned dynamic_library_path=$libdir, got %q", search)
	}
	// Check only the oracle container. dfmgr.c tries the literal name and .so,
	// and returns the original name for ENOENT. Never inspect native files.
	script := `set -eu
libdir=$(pg_config --pkglibdir)
for name in stuntgres_load_contract_missing '$unknown'; do
  test ! -e "$libdir/$name"
  test ! -e "$libdir/$name.so"
done
for name in stuntgres_load_contract_missing 'stuntgres_load_contract_back\slash'; do
  test ! -e "$libdir/plugins/$name"
  test ! -e "$libdir/plugins/$name.so"
done
test ! -e /tmp/stuntgres_load_contract_absent
test ! -e "$PGDATA/stuntgres_load_contract_absent"
test -f "$libdir/plpgsql.so"
`
	code, out, err := fixture.Container.Exec(p.Ctx, []string{"sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("LOAD oracle absence/installed-library precondition exit %d: %s", code, text)
	}
}

func loadSQL(filename string) string {
	return "LOAD '" + strings.ReplaceAll(filename, "'", "''") + "'"
}

func loadDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func loadDenied(filename string) fixture.Diagnostic {
	return loadDiagnostic("42501", "access to library \""+filename+"\" is not allowed")
}

func loadBoundary(b fixture.Backend, filename string) fixture.Diagnostic {
	if b.PG {
		return loadDiagnostic("58P01", "could not access file \""+filename+"\": No such file or directory")
	}
	return loadDiagnostic("0A000", loadUnsupported)
}

func loadExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(query, 0))
}

func loadError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string, want fixture.Diagnostic, parse bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(query, want)
		s.Prepared, s.ErrorStage = prepared, "execute"
		if prepared && parse {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func loadRole(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, name, attributes string) {
	t.Helper()
	loadExec(t, p, b, c, "CREATE ROLE "+name+" "+attributes)
	t.Cleanup(func() {
		if _, err := b.DB.ExecContext(p.Ctx, "DROP ROLE "+name); err != nil {
			t.Error(err)
		}
	})
}

func loadHeld(t *testing.T, p *fixture.Pair, c fixture.Executor, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal("LOAD Prepare must defer authorization and loading:", err)
	}
	t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
}

func loadHeldError(t *testing.T, p *fixture.Pair, b fixture.Backend, held fixture.HeldStatement, want fixture.Diagnostic) {
	t.Helper()
	s := fixture.ErrorDiagnostic(held.SQL, want)
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
}

func loadSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM load_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
}
