package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"testing"
)

type indexMaintenanceFailReader struct{ err error }

func (r indexMaintenanceFailReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceIndexMaintenanceControlsSmoke(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE t(n INT,s TEXT)", 0), referenceExec("INSERT INTO t VALUES(1,'A'),(2,'B'),(NULL,NULL)", 3),
			referenceExec("CREATE INDEX ni ON t(n)", 0), referenceExec("CREATE INDEX ei ON t(lower(s),(n+1))", 0),
			referenceExec("CREATE TABLE q(n INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)", 0), referenceExec("INSERT INTO q VALUES(1),(NULL)", 2),
			referenceExec("ALTER INDEX ei RENAME TO renamed_ei", 0), referenceExec("ALTER INDEX u RENAME TO renamed_u", 0), referenceExec("DROP INDEX ni", 0),
		} {
			run(s)
		}
		var dump bytes.Buffer
		if !b.pg {
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), `CREATE INDEX "ni"`) || strings.Contains(dump.String(), `CREATE INDEX "ei"`) || strings.Contains(dump.String(), `CREATE UNIQUE INDEX`) {
				t.Fatal("obsolete/duplicated definitions", dump.String())
			}
			if !strings.Contains(dump.String(), `CREATE INDEX "renamed_ei"`) || strings.Count(dump.String(), `CONSTRAINT "renamed_u"`) != 1 {
				t.Fatal("missing current definitions", dump.String())
			}
			rows, err := b.db.QueryContext(p.ctx, "SELECT n FROM t WHERE n=1")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			indexMaintenanceRetainedRows(t, rows)
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
		} else {
			// Independent supported SQL replay, not an invented PG Reset/Restore API.
			run(referenceExec("DROP TABLE t", 0))
			run(referenceExec("DROP TABLE q", 0))
			for _, s := range []referenceStep{referenceExec("CREATE TABLE t(n INT,s TEXT)", 0), referenceExec("INSERT INTO t VALUES(1,'A'),(2,'B'),(NULL,NULL)", 3), referenceExec("CREATE INDEX renamed_ei ON t(lower(s),(n+1))", 0), referenceExec("CREATE TABLE q(n INT CONSTRAINT renamed_u UNIQUE NULLS NOT DISTINCT)", 0), referenceExec("INSERT INTO q VALUES(1),(NULL)", 2)} {
				run(s)
			}
		}
		run(referenceSelect("SELECT n,s FROM t ORDER BY n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "B"}, {nil, nil}}))
		run(referenceSelect("SELECT pg_get_indexdef('renamed_u'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX renamed_u ON public.q USING btree (n) NULLS NOT DISTINCT"}}))
		run(referenceExec("INSERT INTO t VALUES(3,'C')", 1))
		run(referenceExec("UPDATE t SET s='Z' WHERE n=2", 1))
		run(referenceExec("DELETE FROM t WHERE n=1", 1))
		run(referencePrepared(referenceExec("REINDEX INDEX renamed_ei", 0)))
		run(referenceExec("REINDEX TABLE q", 0))
		run(referenceSelect("SELECT n,s FROM t ORDER BY n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "Z"}, {int64(3), "C"}, {nil, nil}}))
		run(referenceErrorDiagnostic("INSERT INTO q VALUES(NULL)", uniqueReferenceDuplicate("q", "renamed_u", "Key (n)=(null) already exists.")))
		run(referenceExec("DROP INDEX renamed_ei", 0))
		if !b.pg {
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
		} else {
			run(referenceExec("DROP TABLE t", 0))
			run(referenceExec("DROP TABLE q", 0))
		}
		for _, name := range []string{"t", "q", "renamed_ei", "renamed_u"} {
			s := referenceSelect("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, nil)
			s.args = []any{name}
			run(s)
		}
	})
}

func TestReferenceIndexMaintenanceControlFailures(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(n INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		run(referenceExec("CREATE INDEX ix ON t(n)", 0))
		run(referenceExec("ALTER INDEX ix RENAME TO renamed_ix", 0))
		if !b.pg {
			var before bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
				t.Fatal(err)
			}
			rows, err := b.db.QueryContext(p.ctx, "SELECT n FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			readers := []io.Reader{strings.NewReader("CREATE TABLE replacement(n INT); CREATE INDEX replacement_i ON replacement(n); SELECT FROM"), indexMaintenanceFailReader{err: io.ErrUnexpectedEOF}}
			for _, r := range readers {
				if err := stuntgres.Restore(p.ctx, p.native, r); err == nil {
					t.Fatal("failed Restore accepted")
				}
				var after bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != before.String() {
					t.Fatal("failed Restore changed catalog", err, after.String())
				}
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE replacement(n INT)")); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled Restore", err)
			}
			indexMaintenanceRetainedRows(t, rows)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != before.String() {
				t.Fatal("canceled Restore changed catalog", err, after.String())
			}
		} else {
			run(referenceImplicitError("ALTER INDEX renamed_ix RENAME TO t", referenceDiagnostic{Code: "42P07", Message: `relation "t" already exists`}))
		}
		run(referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referenceExec("REINDEX INDEX renamed_ix", 0))
		run(referenceSelect("SELECT pg_get_indexdef('renamed_ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX renamed_ix ON public.t USING btree (n)"}}))
	})
}

func TestReferenceIndexMaintenanceForeignRestore(t *testing.T) {
	schemaReferenceCleanup(t, "im_portable")
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		retained, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		referenceRunStep(t, p.ctx, retained, b.recorder, referenceExec("CREATE TEMP SEQUENCE retained CACHE 10", 0))
		next := func(n int64) {
			referenceRunStep(t, p.ctx, retained, b.recorder, referenceSelect("SELECT nextval('retained')", []string{"nextval"}, []string{"INT8"}, [][]any{{n}}))
		}
		next(1)
		const stream = `CREATE SCHEMA im_portable; CREATE TABLE im_portable.t(n INT,s TEXT,CONSTRAINT renamed_u UNIQUE(n)); INSERT INTO im_portable.t VALUES(7,'A'); CREATE INDEX renamed_ei ON im_portable.t(lower(s),(n+1));`
		if !b.pg {
			// A separate live source proves incoming OIDs/root identities are remapped,
			// with persistent identity collisions and retained TEMP/cache on destination.
			source := newReferenceFixture(t, false, true)
			if err := stuntgres.Restore(p.ctx, source.native, strings.NewReader(stream)); err != nil {
				t.Fatal(err)
			}
			var incoming, before bytes.Buffer
			if err := stuntgres.Dump(p.ctx, source.native, &incoming); err != nil {
				t.Fatal(err)
			}
			before.Write(incoming.Bytes())
			run(referenceExec("CREATE TABLE collision(n INT)", 0))
			run(referenceExec("CREATE INDEX collision_i ON collision(n)", 0))
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(incoming.String())); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("INSERT INTO im_portable.t VALUES(8,'B')", 1))
			run(referenceExec("REINDEX TABLE im_portable.t", 0))
			var after bytes.Buffer
			if err := stuntgres.Dump(p.ctx, source.native, &after); err != nil || before.String() != after.String() {
				t.Fatal("destination modified source", err, after.String())
			}
		} else {
			for _, s := range []referenceStep{referenceExec("CREATE SCHEMA im_portable", 0), referenceExec("CREATE TABLE im_portable.t(n INT,s TEXT,CONSTRAINT renamed_u UNIQUE(n))", 0), referenceExec("INSERT INTO im_portable.t VALUES(7,'A')", 1), referenceExec("CREATE INDEX renamed_ei ON im_portable.t(lower(s),(n+1))", 0), referenceExec("INSERT INTO im_portable.t VALUES(8,'B')", 1), referenceExec("REINDEX TABLE im_portable.t", 0)} {
				run(s)
			}
		}
		next(2)
		run(referenceSelect("SELECT n,s FROM im_portable.t ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "A"}, {int64(8), "B"}}))
		run(referenceExec("DROP INDEX im_portable.renamed_ei", 0))
		run(referenceExec("DROP TABLE im_portable.t", 0))
		next(3)
	})
}
