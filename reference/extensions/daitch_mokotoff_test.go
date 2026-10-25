package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Goldens are published in PostgreSQL 18 §F.16.2 and REL_18_0
// contrib/fuzzystrmatch/expected/{fuzzystrmatch,fuzzystrmatch_utf8}.out.
// Array element order is significant, unlike unordered SQL result rows.
var daitchPublished = []struct{ input, output string }{
	{"George", "{595000}"}, {"John", "{160000,460000}"},
	{"Bierschbach", "{794575,794574,794750,794740,745750,745740,747500,747400}"},
	{"Schwartzenegger", "{479465}"}, {"Müller", "{689000}"},
	{"Schäfer", "{479000}"}, {"Straßburg", "{294795}"}, {"Éregon", "{095600}"},
	{"gąszczu", "{564000,540000}"}, {"brzęczy", "{794640,794400,746400,744000}"},
	{"ţamas", "{364000,464000}"}, {"țamas", "{364000,464000}"},
	{"Augsburg", "{054795}"}, {"Breuer", "{791900}"}, {"Freud", "{793000}"},
	{"Halberstadt", "{587943,587433}"}, {"Mannheim", "{665600}"},
	{"Chernowitz", "{596740,496740}"}, {"Cherkassy", "{595400,495400}"},
	{"Kleinman", "{586660}"}, {"Nowy Targ", "{673950}"}, {"Berlin", "{798600}"},
	{"Ceniow", "{567000,467000}"}, {"Tsenyuv", "{467000}"},
	{"Holubica", "{587500,587400}"}, {"Golubitsa", "{587400}"},
	{"Przemysl", "{794648,746480}"}, {"Pshemeshil", "{746480}"},
	{"Rosochowaciec", "{945755,945754,945745,945744,944755,944754,944745,944744}"},
	{"Rosokhovatsets", "{945744}"}, {"'OBrien", "{079600}"}, {"O'Brien", "{079600}"},
	{"CJC", "{550000,540000,545000,450000,400000,440000}"},
	{"BESST", "{743000}"}, {"BOUEY", "{710000}"}, {"HANNMANN", "{566600}"},
	{"MCCOYJR", "{651900,654900,654190,654490,645190,645490,641900,644900}"},
	{"ACCURSO", "{059400,054000,054940,054400,045940,045400,049400,044000}"},
}

func daitchCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"dm_installer"})
	fixture.SchemaReferenceCleanup(t, "dm_addon")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE", "DROP SCHEMA IF EXISTS dm_addon CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
					// Database CREATE grants outlive the public-schema reset.
					var roles int
					if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname='dm_installer'").Scan(&roles); err != nil {
						t.Error(err)
					} else if roles != 0 {
						if _, err := c.ExecContext(p.Ctx, "DROP OWNED BY dm_installer"); err != nil {
							t.Error(err)
						}
						if _, err := c.ExecContext(p.Ctx, "DROP ROLE dm_installer"); err != nil {
							t.Error(err)
						}
					}
				}
				clean()
				defer clean()
			}
			body(t, p, b, c)
		})
	}
}

func daitchSelect(q string, value any, pg bool, args ...any) fixture.Step {
	return fixture.ArrayReferenceSelect(q, []string{"daitch_mokotoff"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{value}}, pg, args...)
}

func TestReferenceDaitchMokotoffCodes(t *testing.T) {
	daitchCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.2'", 0))
		q := "SELECT daitch_mokotoff($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range daitchPublished {
			t.Run(tc.input, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				s := daitchSelect(q, tc.output, b.PG, tc.input)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				run(daitchSelect("SELECT daitch_mokotoff("+fixture.ArrayReferenceQuote(tc.input)+")", tc.output, b.PG))
			})
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, daitchSelect(q, nil, b.PG, nil))
		run(daitchSelect("SELECT DAITCH_MOKOTOFF('John'::text)", "{160000,460000}", b.PG))
	})
}

func TestReferenceDaitchMokotoffBoundaries(t *testing.T) {
	daitchCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		// Scanner-derived expectations: unsupported characters are skipped,
		// Latin1 ß maps to ONE S, and combining marks/ł have no transliteration.
		cases := []struct{ input, output any }{
			{nil, nil}, {"", nil}, {" \t\n", nil}, {"-.,![]\\", nil}, {"0123456789", nil},
			{"東京🙂", nil}, {"Жуков", nil}, {"A", "{000000}"},
			{strings.Repeat("B", 1024), "{700000}"},
			{"john", "{160000,460000}"}, {"JOHN", "{160000,460000}"},
			{"S-c h w a r t z e n e g g e r", "{479465}"},
			{"Strasburg", "{294795}"}, {"Mu\u0308ller", "{689000}"},
			{"łJohn", "{160000,460000}"}, {"ß", "{400000}"},
		}
		q := "SELECT daitch_mokotoff($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range cases {
			s := daitchSelect(q, tc.output, b.PG, tc.input)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, q := range []string{"SELECT daitch_mokotoff(NULL)", "SELECT daitch_mokotoff(NULL::text)"} {
			run(daitchSelect(q, nil, b.PG))
			run(fixture.Prepared(daitchSelect(q, nil, b.PG)))
		}
		run(fixture.Exec("CREATE DOMAIN dm_text AS text", 0))
		run(daitchSelect("SELECT daitch_mokotoff('John'::dm_text)", "{160000,460000}", b.PG))
	})
}

func TestReferenceDaitchMokotoffDiagnostics(t *testing.T) {
	t.Run("Public", func(t *testing.T) {
		daitchCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			fail := func(s fixture.Step) {
				s.SelectRows = true
				run(s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				run(s)
			}
			fail(extensionMissing("SELECT daitch_mokotoff('John')", "daitch_mokotoff", "unknown"))
			run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
			for _, tc := range []struct{ q, name, args string }{
				{"SELECT daitch_mokotoff(1)", "daitch_mokotoff", "integer"},
				{"SELECT daitch_mokotoff()", "daitch_mokotoff", ""},
				{"SELECT daitch_mokotoff('John','George')", "daitch_mokotoff", "unknown, unknown"},
				{`SELECT "Daitch_Mokotoff"('John')`, "Daitch_Mokotoff", "unknown"},
				{"SELECT daitch_mokotoff(source => 'John')", "daitch_mokotoff", "source => unknown"},
			} {
				fail(extensionMissing(tc.q, tc.name, tc.args))
			}
			fail(fixture.ImplicitError("SELECT dm_missing.daitch_mokotoff('John')", fixture.Diagnostic{Code: "3F000", Message: `schema "dm_missing" does not exist`, Position: "8"}))
			for _, tc := range []struct{ q, decoration, kind string }{
				{"SELECT daitch_mokotoff(DISTINCT 'John')", "DISTINCT", "an aggregate function"},
				{"SELECT daitch_mokotoff('John' ORDER BY 'John')", "ORDER BY", "an aggregate function"},
				{"SELECT daitch_mokotoff('John') FILTER (WHERE true)", "FILTER", "an aggregate function"},
				{"SELECT daitch_mokotoff('John') OVER ()", "OVER", "a window function nor an aggregate function"},
			} {
				fail(fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42809", Message: tc.decoration + " specified, but daitch_mokotoff is not " + tc.kind, Position: "8"}))
			}
			daitchQualifiedDecorations(t, p, b, c, "public.daitch_mokotoff")
			run(daitchSelect("SELECT daitch_mokotoff(ALL 'John')", "{160000,460000}", b.PG))
			run(fixture.Exec("SET search_path TO pg_catalog", 0))
			fail(extensionMissing("SELECT daitch_mokotoff('John')", "daitch_mokotoff", "unknown"))
			run(daitchSelect("SELECT public.daitch_mokotoff('John')", "{160000,460000}", b.PG))
			run(fixture.Exec("RESET search_path", 0))
		})
	})
	t.Run("Relocated", func(t *testing.T) {
		daitchCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA dm_addon", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA dm_addon VERSION '1.2'", 0))
			daitchQualifiedDecorations(t, p, b, c, "dm_addon.daitch_mokotoff")
		})
	})
}

// REL_18_0 parse_func.c uses NameListToString(funcname), preserving supplied
// schema qualification in each scalar-decoration primary. Expect independently
// specified whole diagnostics, never text derived from native failures.
func daitchQualifiedDecorations(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	for _, tc := range []struct{ suffix, decoration, kind string }{
		{"(DISTINCT 'John')", "DISTINCT", "an aggregate function"},
		{"('John' ORDER BY 'John')", "ORDER BY", "an aggregate function"},
		{"('John') FILTER (WHERE true)", "FILTER", "an aggregate function"},
		{"('John') OVER ()", "OVER", "a window function nor an aggregate function"},
	} {
		s := fixture.ImplicitError("SELECT "+name+tc.suffix, fixture.Diagnostic{Code: "42809", Message: tc.decoration + " specified, but " + name + " is not " + tc.kind, Position: "8"})
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		s = fixture.Prepared(s)
		s.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
