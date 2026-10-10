package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceSequencePreparedSourceStream(t *testing.T) {
	for _, schema := range []string{"a", "b", "unrelated"} {
		schemaReferenceCleanup(t, schema)
	}
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			value := func(s *sql.Stmt, want int64, args ...any) {
				t.Helper()
				var got int64
				if err := s.QueryRowContext(p.ctx, args...).Scan(&got); err != nil || got != want {
					t.Fatal(got, want, err)
				}
			}
			failure := func(s *sql.Stmt, code, message, position string) {
				t.Helper()
				var n int64
				want := controlsReferenceDiagnostic(code, message)
				want.Position = position
				referenceAssertDiagnostic(t, s.QueryRowContext(p.ctx).Scan(&n), &want)
			}
			w.exec("CREATE SEQUENCE s START 14 CACHE 1")
			s := w.hold("SELECT nextval('s')")
			w.exec("ALTER SEQUENCE s RENAME TO identity")
			failure(s, "42P01", `relation "s" does not exist`, "16")
			value(w.hold("SELECT nextval($1)"), 14, "identity")
			w.exec("CREATE SEQUENCE s START 90")
			curr := w.hold("SELECT currval('s')")
			failure(curr, "55000", `currval of sequence "s" is not yet defined in this session`, "")
			value(s, 90)
			value(curr, 90)
			var identity string
			if err := w.c.QueryRowContext(p.ctx, "SELECT 'identity'::regclass").Scan(&identity); err != nil || identity != "identity" {
				t.Fatal(identity, err)
			}
			w.exec("ALTER SEQUENCE identity RENAME TO old_identity")
			value(w.hold("SELECT nextval($1)"), 15, "old_identity")
			w.exec("DROP SEQUENCE old_identity")
			w.exec("CREATE SEQUENCE identity START 500")
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s START 10", "CREATE SEQUENCE b.s START 100", "SET search_path TO a,b"} {
				w.exec(q)
			}
			path := w.hold("SELECT nextval('s')")
			value(path, 10)
			w.exec("SET search_path TO b,a")
			value(path, 100)
			w.exec("CREATE SEQUENCE a.callback START 10 CACHE 1")
			callback := w.hold("SELECT nextval('callback')")
			value(callback, 10)
			w.exec("CREATE SEQUENCE b.callback START 100 CACHE 1")
			value(callback, 11)
			w.exec("CREATE SCHEMA unrelated")
			value(callback, 100)
			sequenceProbeValue(t, p.ctx, w.c, "SELECT last_value FROM a.callback", 11)
			sequenceProbeValue(t, p.ctx, w.c, "SELECT last_value FROM b.callback", 100)
			w.exec("CREATE TEMP SEQUENCE local_seq CACHE 10")
			temp := w.hold("SELECT nextval('pg_temp.local_seq')")
			remembered := w.hold("SELECT currval('pg_temp.local_seq')")
			value(temp, 1)
			persistent := w.hold("SELECT nextval('public.identity')")
			if !backend.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				value(remembered, 1)
				value(temp, 2)
				var n int64
				if err := persistent.QueryRowContext(p.ctx).Scan(&n); err == nil {
					t.Fatal("persistent prepared source escaped native replacement epoch fence")
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				value(remembered, 2)
				value(temp, 3)
			} else {
				// Native replacement APIs have no PostgreSQL equivalent. Independently
				// require the same supported held temporary consumers without them.
				value(remembered, 1)
				value(temp, 2)
				value(remembered, 2)
				value(temp, 3)
			}
		})
	}
}
