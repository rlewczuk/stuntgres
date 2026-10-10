package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

type concurrentIndexCountingWriter struct{ calls, bytes int }

func (w *concurrentIndexCountingWriter) Write(p []byte) (int, error) {
	w.calls++
	w.bytes += len(p)
	return len(p), nil
}
func concurrentIndexDumpInvalid(t *testing.T, p *referencePair, name string) {
	t.Helper()
	before, lookupErr := stuntgres.InspectIndex(p.ctx, p.native, "public", name)
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	var out concurrentIndexCountingWriter
	err := stuntgres.Dump(p.ctx, p.native, &out)
	want := "stuntgres: dump cannot serialize invalid index \"public\".\"" + name + "\""
	if err == nil || err.Error() != want || out.calls != 0 || out.bytes != 0 {
		t.Fatalf("invalid Dump: %v calls=%d bytes=%d", err, out.calls, out.bytes)
	}
	after, lookupErr := stuntgres.InspectIndex(p.ctx, p.native, "public", name)
	if lookupErr != nil || after != before {
		t.Fatal("Dump changed state", lookupErr, before, after)
	}
}

func TestReferenceConcurrentIndexControls(t *testing.T) {
	t.Run("rebuild", func(t *testing.T) {
		concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			c := concurrentIndexConn(t, p, b)
			concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
			concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1),(1)", 2)
			want := concurrentIndexUnique("t", "ui", "Key (n)=(1) is duplicated.", true)
			concurrentIndexError(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", want)
			concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
			if !b.pg {
				concurrentIndexDumpInvalid(t, p, "ui")
			}
			concurrentIndexExec(t, p, b, c, "ALTER TABLE t ADD COLUMN payload TEXT", 0)
			concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
			concurrentIndexError(t, p, b, c, "REINDEX INDEX ui", want)
			concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
			concurrentIndexExec(t, p, b, c, "DELETE FROM t WHERE n=1", 2)
			concurrentIndexExec(t, p, b, c, "INSERT INTO t(n) VALUES(1)", 1)
			concurrentIndexExec(t, p, b, c, "REINDEX TABLE t", 0)
			concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			concurrentIndexError(t, p, b, c, "INSERT INTO t(n) VALUES(1)", concurrentIndexUnique("t", "ui", "Key (n)=(1) already exists.", false))
		})
	})
	t.Run("ready-invalid-dump", func(t *testing.T) {
		concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
			ap, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, builder)
			concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
			concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
			concurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
			concurrentIndexRows(t, p, b, a, "SELECT n FROM t ORDER BY n", 1)
			w := concurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", false)
			concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
			if !b.pg {
				concurrentIndexDumpInvalid(t, p, "ui")
			}
			concurrentIndexExec(t, p, b, a, "COMMIT", 0)
			if err := w.join(t, p); err != nil {
				t.Fatal(err)
			}
		})
	})
	t.Run("replay", func(t *testing.T) { concurrentIndexScenario(t, concurrentIndexReplay) })
	t.Run("inspect-errors", func(t *testing.T) {
		concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			c := concurrentIndexConn(t, p, b)
			concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
			concurrentIndexExec(t, p, b, c, "CREATE INDEX ix ON t(n)", 0)
			concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			if b.pg {
				return
			}
			s, err := stuntgres.InspectIndex(p.ctx, p.native, "public", "ix")
			if err != nil {
				t.Fatal(err)
			}
			s.Ready = false
			concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			for _, tc := range []struct{ schema, name, want string }{{"absent", "ix", "stuntgres: index schema \"absent\" does not exist"}, {"public", "absent", "stuntgres: index \"public\".\"absent\" does not exist"}, {"public", "t", "stuntgres: relation \"public\".\"t\" is not an index"}} {
				_, err := stuntgres.InspectIndex(p.ctx, p.native, tc.schema, tc.name)
				if err == nil || err.Error() != tc.want {
					t.Fatalf("InspectIndex: %v want %s", err, tc.want)
				}
			}
			_, err = stuntgres.InspectIndex(p.ctx, nil, "public", "ix")
			if err == nil || err.Error() != "stuntgres: nil database" {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			_, err = stuntgres.InspectIndex(ctx, p.native, "public", "ix")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			_, err = stuntgres.InspectIndex(p.ctx, b.db, "public", "ix")
			if err == nil || err.Error() != "stuntgres: controls require a native stuntgres database, not a foreign or wrapped driver" {
				t.Fatal(err)
			}
		})
	})
}

func concurrentIndexReplay(t *testing.T, p *referencePair, b referenceBackend) {
	c := concurrentIndexConn(t, p, b)
	concurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,n INTEGER,payload TEXT)", 0)
	concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('A',1,'p')", 1)
	concurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n DESC NULLS FIRST) INCLUDE(payload)", 0)
	concurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(lower(v) COLLATE \"C\" text_ops) INCLUDE(payload)", 0)
	want := concurrentIndexUnique("t", "ui", "Key (lower(v))=(a) already exists.", false)
	concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('a',2,'q')", want)
	if b.pg {
		// Canonical SQL that native Dump emits: ordinary UNIQUE is a PostgreSQL replay control.
		concurrentIndexExec(t, p, b, c, "DROP TABLE t", 0)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,n INTEGER,payload TEXT)", 0)
		concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('A',1,'p')", 1)
		concurrentIndexExec(t, p, b, c, "CREATE INDEX ix ON public.t USING btree (n DESC NULLS FIRST) INCLUDE(payload)", 0)
		concurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX ui ON public.t USING btree (lower(v) COLLATE \"C\" text_ops) INCLUDE(payload)", 0)
	} else {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		definition := `CREATE UNIQUE INDEX "ui" ON "public"."t" USING btree ((pg_catalog.lower("v")) COLLATE "pg_catalog"."C" "pg_catalog"."text_ops" ASC NULLS LAST) INCLUDE ("payload");`
		if !strings.Contains(dump.String(), definition) {
			t.Fatalf("missing owned definition %s: %s", definition, dump.String())
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		// Replay the actual unchanged emitted SQL on the pinned server in real mode.
		if p.postgres != nil {
			if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE IF EXISTS t CASCADE"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			_, err := p.postgres.ExecContext(p.ctx, "INSERT INTO t VALUES('a',2,'q')")
			referenceAssertDiagnostic(t, err, &want)
			if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE t CASCADE"); err != nil {
				t.Fatal(err)
			}
		}
	}
	concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
	referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT pg_get_indexdef('ui'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE UNIQUE INDEX ui ON public.t USING btree (lower(v) COLLATE "C") INCLUDE (payload)`}}))
	referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT pg_get_indexdef('ix'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE INDEX ix ON public.t USING btree (n DESC) INCLUDE (payload)`}}))
	concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1)
	concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('a',2,'q')", want)
	concurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX other ON t(n) NULLS NOT DISTINCT", 0)
	concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('b',1,'q')", concurrentIndexUnique("t", "other", "Key (n)=(1) already exists.", false))
}
