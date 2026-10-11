package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexControls(t *testing.T) {
	t.Run("rebuild", func(t *testing.T) {
		fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := fixture.ConcurrentIndexConn(t, p, b)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1),(1)", 2)
			want := fixture.ConcurrentIndexUnique("t", "ui", "Key (n)=(1) is duplicated.", true)
			fixture.ConcurrentIndexError(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", want)
			fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
			if !b.PG {
				fixture.ConcurrentIndexDumpInvalid(t, p, "ui")
			}
			fixture.ConcurrentIndexExec(t, p, b, c, "ALTER TABLE t ADD COLUMN payload TEXT", 0)
			fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
			fixture.ConcurrentIndexError(t, p, b, c, "REINDEX INDEX ui", want)
			fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
			fixture.ConcurrentIndexExec(t, p, b, c, "DELETE FROM t WHERE n=1", 2)
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t(n) VALUES(1)", 1)
			fixture.ConcurrentIndexExec(t, p, b, c, "REINDEX TABLE t", 0)
			fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t(n) VALUES(1)", fixture.ConcurrentIndexUnique("t", "ui", "Key (n)=(1) already exists.", false))
		})
	})
	t.Run("ready-invalid-dump", func(t *testing.T) {
		fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
			ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
			fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
			fixture.ConcurrentIndexRows(t, p, b, a, "SELECT n FROM t ORDER BY n", 1)
			w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", false)
			fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
			if !b.PG {
				fixture.ConcurrentIndexDumpInvalid(t, p, "ui")
			}
			fixture.ConcurrentIndexExec(t, p, b, a, "COMMIT", 0)
			if err := w.Join(t, p); err != nil {
				t.Fatal(err)
			}
		})
	})
	t.Run("replay", func(t *testing.T) { fixture.ConcurrentIndexScenario(t, concurrentIndexReplay) })
	t.Run("inspect-errors", func(t *testing.T) {
		fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := fixture.ConcurrentIndexConn(t, p, b)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX ix ON t(n)", 0)
			fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			if b.PG {
				return
			}
			s, err := stuntgres.InspectIndex(p.Ctx, p.Native, "public", "ix")
			if err != nil {
				t.Fatal(err)
			}
			s.Ready = false
			fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			for _, tc := range []struct{ schema, name, want string }{{"absent", "ix", "stuntgres: index schema \"absent\" does not exist"}, {"public", "absent", "stuntgres: index \"public\".\"absent\" does not exist"}, {"public", "t", "stuntgres: relation \"public\".\"t\" is not an index"}} {
				_, err := stuntgres.InspectIndex(p.Ctx, p.Native, tc.schema, tc.name)
				if err == nil || err.Error() != tc.want {
					t.Fatalf("InspectIndex: %v want %s", err, tc.want)
				}
			}
			_, err = stuntgres.InspectIndex(p.Ctx, nil, "public", "ix")
			if err == nil || err.Error() != "stuntgres: nil database" {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err = stuntgres.InspectIndex(ctx, p.Native, "public", "ix")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			_, err = stuntgres.InspectIndex(p.Ctx, b.DB, "public", "ix")
			if err == nil || err.Error() != "stuntgres: controls require a native stuntgres database, not a foreign or wrapped driver" {
				t.Fatal(err)
			}
		})
	})
}

func concurrentIndexReplay(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	c := fixture.ConcurrentIndexConn(t, p, b)
	fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,n INTEGER,payload TEXT)", 0)
	fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('A',1,'p')", 1)
	fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n DESC NULLS FIRST) INCLUDE(payload)", 0)
	fixture.ConcurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(lower(v) COLLATE \"C\" text_ops) INCLUDE(payload)", 0)
	want := fixture.ConcurrentIndexUnique("t", "ui", "Key (lower(v))=(a) already exists.", false)
	fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('a',2,'q')", want)
	if b.PG {
		// Canonical SQL that native Dump emits: ordinary UNIQUE is a PostgreSQL replay control.
		fixture.ConcurrentIndexExec(t, p, b, c, "DROP TABLE t", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,n INTEGER,payload TEXT)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('A',1,'p')", 1)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX ix ON public.t USING btree (n DESC NULLS FIRST) INCLUDE(payload)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX ui ON public.t USING btree (lower(v) COLLATE \"C\" text_ops) INCLUDE(payload)", 0)
	} else {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		definition := `CREATE UNIQUE INDEX "ui" ON "public"."t" USING btree ((pg_catalog.lower("v")) COLLATE "pg_catalog"."C" "pg_catalog"."text_ops" ASC NULLS LAST) INCLUDE ("payload");`
		if !strings.Contains(dump.String(), definition) {
			t.Fatalf("missing owned definition %s: %s", definition, dump.String())
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		// Replay the actual unchanged emitted SQL on the pinned server in real mode.
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE IF EXISTS t CASCADE"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			_, err := p.Postgres.ExecContext(p.Ctx, "INSERT INTO t VALUES('a',2,'q')")
			fixture.AssertDiagnostic(t, err, &want)
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE t CASCADE"); err != nil {
				t.Fatal(err)
			}
		}
	}
	fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_get_indexdef('ui'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE UNIQUE INDEX ui ON public.t USING btree (lower(v) COLLATE "C") INCLUDE (payload)`}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_get_indexdef('ix'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE INDEX ix ON public.t USING btree (n DESC) INCLUDE (payload)`}}))
	fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1)
	fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('a',2,'q')", want)
	fixture.ConcurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX other ON t(n) NULLS NOT DISTINCT", 0)
	fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('b',1,'q')", fixture.ConcurrentIndexUnique("t", "other", "Key (n)=(1) already exists.", false))
}
