package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestReferencePGDependControls(t *testing.T) {
	schemaReferenceCleanup(t, "pgdep_controls")
	p := newReferenceScenario(t)
	var nativeDump string
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependCore(t, p, b, "pgdep_controls")
		pgDependExec(t, p, b, "CREATE TABLE p(a INT)", "CREATE TABLE consumer(c BOOL DEFAULT ('p'::regclass IS NOT NULL))")
		check := func(b referenceBackend) {
			t.Helper()
			pgDependCheckCore(t, p, b, "pgdep_controls")
			provider := pgDependRelation(t, b, "pgdep_controls.p")
			consumer := pgDependRelation(t, b, "pgdep_controls.consumer")
			d := pgDependDefinition(t, b, consumer, 1)
			pgDependBag(t, p, b, 2604, d, pgDependEdge(2604, d, 0, 1259, consumer, 1, "a"), pgDependEdge(2604, d, 0, 1259, provider, 0, "n"))
		}
		check(b)
		if !b.pg { // Native APIs have no invented PostgreSQL equivalent.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			nativeDump = dump.String()
			if strings.Contains(nativeDump, "INSERT INTO \"pg_catalog\".\"pg_depend\"") {
				t.Fatal("derived edges serialized", nativeDump)
			}
			table := pgDependRelation(t, b, "pgdep_controls.t")
			ns := pgDependNamespace(t, b, table)
			old, err := b.db.QueryContext(p.ctx, "SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", table)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(nativeDump+"INSERT INTO pgdep_controls.t(missing) VALUES(1);")); err == nil {
				t.Fatal("late invalid restore succeeded")
			} else {
				referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" of relation \"t\" does not exist", Position: referencePosition(utf8.RuneCountInString(nativeDump+"INSERT INTO pgdep_controls.t(") + 1)})
			}
			check(b)
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.native, strings.NewReader(nativeDump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check(b)
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			pgDependBag(t, p, b, 1259, table)
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(nativeDump)); err != nil {
				t.Fatal(err)
			}
			check(b)
			pgDependRetained(t, old, [][]any{pgDependEdge(1259, table, 0, 2615, ns, 0, "n")})
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/foreign")
			if err != nil {
				t.Fatal(err)
			}
			foreign := sql.OpenDB(connector)
			defer foreign.Close()
			if err := stuntgres.Restore(p.ctx, foreign, strings.NewReader(nativeDump)); err != nil {
				t.Fatal(err)
			}
			fb := b
			fb.db = foreign
			fb.recorder = nil
			// Foreign pool is unwrapped: verify public relationships directly, not
			// pretend it owns the source pool's middleware records.
			ft := pgDependRelation(t, fb, "pgdep_controls.t")
			fn := pgDependNamespace(t, fb, ft)
			rows, err := foreign.QueryContext(p.ctx, "SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", ft)
			if err != nil {
				t.Fatal(err)
			}
			pgDependRetained(t, rows, [][]any{pgDependEdge(1259, ft, 0, 2615, fn, 0, "n")})
			fp := pgDependRelation(t, fb, "pgdep_controls.p")
			fc := pgDependRelation(t, fb, "pgdep_controls.consumer")
			fd := pgDependDefinition(t, fb, fc, 1)
			rows, err = foreign.QueryContext(p.ctx, "SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", fd)
			if err != nil {
				t.Fatal(err)
			}
			pgDependRetained(t, rows, [][]any{pgDependEdge(2604, fd, 0, 1259, fc, 1, "a"), pgDependEdge(2604, fd, 0, 1259, fp, 0, "n")})
		}
	})
	if nativeDump != "" && p.postgres != nil {
		b := p.backends()[1]
		pgDependExec(t, p, b, "DROP SCHEMA pgdep_controls CASCADE")
		if _, err := b.db.ExecContext(p.ctx, nativeDump); err != nil {
			t.Fatal("pinned PG rejected actual native Dump", err)
		}
		pgDependCheckCore(t, p, b, "pgdep_controls")
	}
}
