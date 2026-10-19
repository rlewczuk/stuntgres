package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDomainDefaultCallers(t *testing.T) {
	domainDefaultScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) {
			// Installed lib/pq reports MERGE affected-count zero; retain the native
			// exact count and independent stored-row assertions on both backends.
			if b.PG && strings.HasPrefix(s.Query, "MERGE ") {
				s.Affected = 0
			}
			if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) }) {
				t.FailNow()
			}
		}
		for _, q := range []string{
			"CREATE DOMAIN domain_s.caller AS integer DEFAULT 22",
			"CREATE DOMAIN domain_s.caller_required AS integer NOT NULL",
			"CREATE TABLE domain_s.callers(id integer,v domain_s.caller,w domain_s.caller DEFAULT 44,z domain_s.caller DEFAULT NULL)",
			"CREATE VIEW domain_s.caller_view AS SELECT id,v,w,z FROM domain_s.callers",
			"CREATE TABLE domain_s.merge_source(id integer)",
			"INSERT INTO domain_s.merge_source VALUES(3)",
		} {
			n := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				n = 1
			}
			run(fixture.Exec(q, n))
		}
		run(fixture.Select("SELECT v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT id FROM domain_s.merge_source", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("DELETE FROM domain_s.callers", int64(0)))
			steps := []fixture.Step{
				fixture.Exec("INSERT INTO domain_s.callers(id) SELECT v FROM public.one", 1),
				fixture.Exec("UPDATE domain_s.callers SET v=DEFAULT,w=DEFAULT,z=DEFAULT", 1),
				fixture.Exec("INSERT INTO domain_s.caller_view(id,v) VALUES(2,DEFAULT)", 1),
				fixture.Exec("UPDATE domain_s.caller_view SET v=DEFAULT WHERE id=2", 1),
				fixture.Exec("MERGE INTO domain_s.callers t USING domain_s.merge_source s ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,DEFAULT)", 1),
			}
			for _, s := range steps {
				s.Prepared = prepared
				run(s)
			}
			run(fixture.Exec("DELETE FROM domain_s.callers WHERE id=3", 1))
			omitted := fixture.Exec("MERGE INTO domain_s.callers t USING domain_s.merge_source s ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)", 1)
			omitted.Prepared = prepared
			run(omitted)
			// Pinned18.6 view insertion uses declared domain defaults for omitted
			// view columns, unlike direct physical-column defaults on rows 1/3.
			run(fixture.Select("SELECT id,v,w,z FROM domain_s.callers ORDER BY id", []string{"id", "v", "w", "z"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(22), int64(44), nil}, {int64(2), int64(22), int64(22), int64(22)}, {int64(3), int64(22), int64(44), nil}}))
			run(fixture.Exec("DELETE FROM domain_s.callers", 3))
		}
		run(fixture.Exec("CREATE TABLE domain_s.backfill(id integer)", 0))
		run(fixture.Exec("INSERT INTO domain_s.backfill VALUES(1),(2)", 2))
		run(fixture.Exec("ALTER TABLE domain_s.backfill ADD COLUMN v domain_s.caller", 0))
		run(fixture.Exec("ALTER TABLE domain_s.backfill ADD COLUMN w domain_s.caller DEFAULT 44", 0))
		run(fixture.Exec("ALTER TABLE domain_s.backfill ADD COLUMN z domain_s.caller DEFAULT NULL", 0))
		run(fixture.Exec("INSERT INTO domain_s.backfill(id) VALUES(3)", 1))
		run(fixture.Select("SELECT id,v,w,z FROM domain_s.backfill ORDER BY id", []string{"id", "v", "w", "z"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(22), int64(44), nil}, {int64(2), int64(22), int64(44), nil}, {int64(3), int64(22), int64(44), nil}}))
		run(fixture.Exec("CREATE TABLE domain_s.add_empty(id integer)", 0))
		run(fixture.Exec("ALTER TABLE domain_s.add_empty ADD COLUMN v domain_s.caller_required", 0))
		run(domainDefaultFailure("INSERT INTO domain_s.add_empty(id) VALUES(1)", domainNotNullDiagnostic("domain_s", "caller_required"), false, false))
		run(domainDefaultFailure("ALTER TABLE domain_s.backfill ADD COLUMN required domain_s.caller_required", domainNotNullDiagnostic("domain_s", "caller_required"), false, false))
		run(fixture.Exec("ALTER TABLE domain_s.backfill ADD COLUMN required integer DEFAULT 9", 0))
		run(fixture.Select("SELECT required FROM domain_s.backfill ORDER BY id", []string{"required"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(9)}, {int64(9)}}))
		run(fixture.Exec("CREATE SEQUENCE domain_s.draws START 100", 0))
		run(fixture.Exec("CREATE DOMAIN domain_s.draw AS bigint DEFAULT nextval('domain_s.draws'::regclass)", 0))
		run(fixture.Exec("CREATE TABLE domain_s.draw_rows(id integer,v domain_s.draw)", 0))
		run(fixture.Exec("INSERT INTO domain_s.draw_rows(id) VALUES(1),(2)", 2))
		run(fixture.Select("SELECT v FROM domain_s.draw_rows ORDER BY id", []string{"v"}, []string{"INT8"}, [][]any{{int64(100)}, {int64(101)}}))
		run(fixture.Exec("CREATE TABLE domain_s.draw_add(id integer)", 0))
		run(fixture.Exec("INSERT INTO domain_s.draw_add VALUES(1),(2)", 2))
		run(fixture.Exec("ALTER TABLE domain_s.draw_add ADD COLUMN v domain_s.draw", 0))
		run(fixture.Select("SELECT v FROM domain_s.draw_add ORDER BY id", []string{"v"}, []string{"INT8"}, [][]any{{int64(102)}, {int64(103)}}))
		db := p.Native
		if b.PG {
			db = p.CopyPostgres
		}
		transport, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer transport.Close()
		copyOK := func(q, input string, n int64) {
			t.Helper()
			got, err := domainCheckCopyFrom(p, b.PG, transport, q, strings.NewReader(input))
			if err != nil || got != (stuntgres.CopyResult{Count: n, CommandTag: "COPY 2"}) {
				t.Fatalf("COPY result=%+v error=%v", got, err)
			}
		}
		copyOK("COPY domain_s.callers(id) FROM STDIN", "4\n5\n", 2)
		copyOK("COPY domain_s.callers(id,v,w,z) FROM STDIN WITH (DEFAULT 'use_default')", "6\tuse_default\tuse_default\tuse_default\n7\tuse_default\tuse_default\tuse_default\n", 2)
		run(fixture.Select("SELECT id,v,w,z FROM domain_s.callers ORDER BY id", []string{"id", "v", "w", "z"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(4), int64(22), int64(44), nil}, {int64(5), int64(22), int64(44), nil}, {int64(6), int64(22), int64(44), nil}, {int64(7), int64(22), int64(44), nil}}))
		copyOK("COPY domain_s.draw_rows(id) FROM STDIN", "3\n4\n", 2)
		run(fixture.Select("SELECT v FROM domain_s.draw_rows ORDER BY id", []string{"v"}, []string{"INT8"}, [][]any{{int64(100)}, {int64(101)}, {int64(104)}, {int64(105)}}))
		run(fixture.Exec("CREATE TABLE domain_s.copy_required(v domain_s.caller_required)", 0))
		run(fixture.Exec("INSERT INTO domain_s.copy_required VALUES(9)", 1))
		got, err := domainCheckCopyFrom(p, b.PG, transport, "COPY domain_s.copy_required FROM STDIN", strings.NewReader("7\n\\N\n"))
		if got != (stuntgres.CopyResult{}) {
			t.Errorf("failed COPY published %+v", got)
		}
		d := domainNotNullDiagnostic("domain_s", "caller_required")
		d.Context = "COPY copy_required, line 2, column v: null input"
		fixture.AssertDiagnostic(t, err, &d)
		run(fixture.Select("SELECT v FROM domain_s.copy_required", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
	})
}
