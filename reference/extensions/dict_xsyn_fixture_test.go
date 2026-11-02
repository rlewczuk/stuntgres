package reference_test

import (
	"database/sql"
	"path"
	"testing"

	"stuntgres/reference/fixture"
)

const xsynLexizeSQL = "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
const xsynAll = "{supernova,sn,sne,1987a}"
const xsynSynonyms = "{sn,sne,1987a}"

func xsynCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"xsyn_owner", "xsyn_denied"})
	fixture.SchemaReferenceCleanup(t, "xsyn_addon", "xsyn_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func xsynRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func xsynExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, count int64, args ...any) {
	t.Helper()
	xsynRun(t, p, b, e, fixture.Exec(q, count, args...))
}

func xsynSelect(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, columns, types []string, rows [][]any, args ...any) {
	t.Helper()
	s := fixture.Select(q, columns, types, rows, args...)
	xsynRun(t, p, b, e, s)
	xsynRun(t, p, b, e, fixture.Prepared(s))
}

func xsynLexemes(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, want, dictionary, input any) {
	t.Helper()
	fixture.TSTemplateLexemes(t, p, e, b.Recorder, b.PG, xsynLexizeSQL, want, dictionary, input)
}

func xsynHeld(t *testing.T, p *fixture.Pair, c *sql.Conn) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, xsynLexizeSQL)
	if err != nil {
		t.Fatal(err)
	}
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: xsynLexizeSQL}
}

func xsynOptions(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, want any) {
	t.Helper()
	xsynSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid=$1::regdictionary::oid", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{want}}, name)
}

// pg_config is the server's genuine installation metadata, not an error-derived
// prefix. No file is installed merely to discover SHAREDIR.
func xsynResourceDiagnostic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, basename string) (fixture.Diagnostic, fixture.Diagnostic) {
	t.Helper()
	name := basename + ".rules"
	message := func(file string) string {
		return "could not open synonym file \"" + file + "\": No such file or directory"
	}
	native := fixture.ControlsReferenceDiagnostic("F0000", message(name))
	pg := native
	if b.PG {
		var shared string
		if err := c.QueryRowContext(p.Ctx, "SELECT setting FROM pg_config WHERE name='SHAREDIR'").Scan(&shared); err != nil {
			t.Fatal("oracle SHAREDIR", err)
		}
		if !path.IsAbs(shared) {
			t.Fatalf("oracle SHAREDIR is not absolute: %q", shared)
		}
		pg = fixture.ControlsReferenceDiagnostic("F0000", message(path.Join(shared, "tsearch_data", name)))
	}
	return native, pg
}
