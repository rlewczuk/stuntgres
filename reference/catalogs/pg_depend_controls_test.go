package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferencePGDependControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgdep_controls")
	p := fixture.NewScenario(t)
	var nativeDump string
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependCore(t, p, b, "pgdep_controls")
		fixture.PgDependExec(t, p, b, "CREATE TABLE p(a INT)", "CREATE TABLE consumer(c BOOL DEFAULT ('p'::regclass IS NOT NULL))")
		check := func(b fixture.Backend) {
			t.Helper()
			fixture.PgDependCheckCore(t, p, b, "pgdep_controls")
			provider := fixture.PgDependRelation(t, b, "pgdep_controls.p")
			consumer := fixture.PgDependRelation(t, b, "pgdep_controls.consumer")
			d := fixture.PgDependDefinition(t, b, consumer, 1)
			fixture.PgDependBag(t, p, b, 2604, d, fixture.PgDependEdge(2604, d, 0, 1259, consumer, 1, "a"), fixture.PgDependEdge(2604, d, 0, 1259, provider, 0, "n"))
		}
		check(b)
		if !b.PG { // Native APIs have no invented PostgreSQL equivalent.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			nativeDump = dump.String()
			if strings.Contains(nativeDump, "INSERT INTO \"pg_catalog\".\"pg_depend\"") {
				t.Fatal("derived edges serialized", nativeDump)
			}
			table := fixture.PgDependRelation(t, b, "pgdep_controls.t")
			ns := fixture.PgDependNamespace(t, b, table)
			old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", table)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(nativeDump+"INSERT INTO pgdep_controls.t(missing) VALUES(1);")); err == nil {
				t.Fatal("late invalid restore succeeded")
			} else {
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" of relation \"t\" does not exist", Position: fixture.Position(utf8.RuneCountInString(nativeDump+"INSERT INTO pgdep_controls.t(") + 1)})
			}
			check(b)
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(nativeDump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check(b)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.PgDependBag(t, p, b, 1259, table)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(nativeDump)); err != nil {
				t.Fatal(err)
			}
			check(b)
			fixture.PgDependRetained(t, old, [][]any{fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n")})
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/foreign")
			if err != nil {
				t.Fatal(err)
			}
			foreign := sql.OpenDB(connector)
			defer foreign.Close()
			if err := stuntgres.Restore(p.Ctx, foreign, strings.NewReader(nativeDump)); err != nil {
				t.Fatal(err)
			}
			fb := b
			fb.DB = foreign
			fb.Recorder = nil
			// Foreign pool is unwrapped: verify public relationships directly, not
			// pretend it owns the source pool's middleware records.
			ft := fixture.PgDependRelation(t, fb, "pgdep_controls.t")
			fn := fixture.PgDependNamespace(t, fb, ft)
			rows, err := foreign.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", ft)
			if err != nil {
				t.Fatal(err)
			}
			fixture.PgDependRetained(t, rows, [][]any{fixture.PgDependEdge(1259, ft, 0, 2615, fn, 0, "n")})
			fp := fixture.PgDependRelation(t, fb, "pgdep_controls.p")
			fc := fixture.PgDependRelation(t, fb, "pgdep_controls.consumer")
			fd := fixture.PgDependDefinition(t, fb, fc, 1)
			rows, err = foreign.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", fd)
			if err != nil {
				t.Fatal(err)
			}
			fixture.PgDependRetained(t, rows, [][]any{fixture.PgDependEdge(2604, fd, 0, 1259, fc, 1, "a"), fixture.PgDependEdge(2604, fd, 0, 1259, fp, 0, "n")})
		}
	})
	if nativeDump != "" && p.Postgres != nil {
		b := p.Backends()[1]
		fixture.PgDependExec(t, p, b, "DROP SCHEMA pgdep_controls CASCADE")
		if _, err := b.DB.ExecContext(p.Ctx, nativeDump); err != nil {
			t.Fatal("pinned PG rejected actual native Dump", err)
		}
		fixture.PgDependCheckCore(t, p, b, "pgdep_controls")
	}
}
