package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateNoData(t *testing.T) {
	for _, body := range []string{"SELECT 1/0 AS x", "SELECT 'bad'::text::integer AS x", "SELECT (SELECT 1/0) AS x"} {
		t.Run(body, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					_, run := fixture.QueryTableCreateSession(t, p, b)
					run(fixture.Exec("CREATE TABLE qc_nodata AS "+body+" WITH NO DATA", 0))
					run(fixture.Select("SELECT x FROM qc_nodata", []string{"x"}, []string{"INT4"}, nil))
				})
			}
		})
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			fixture.QueryTableCreateSource(run)
			run(fixture.Exec("CREATE SEQUENCE qc_draw START 10", 0))
			run(fixture.Prepared(fixture.Exec("CREATE TABLE qc_no_draw AS SELECT nextval('qc_draw') AS n FROM qc_source WITH NO DATA", 0)))
			run(fixture.Select("SELECT nextval('qc_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
			run(fixture.Exec("CREATE TABLE qc_drawn AS SELECT nextval('qc_draw') AS n FROM qc_source", 3))
			run(fixture.Select("SELECT n FROM qc_drawn ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(11)}, {int64(12)}, {int64(13)}}))
			run(fixture.Select("SELECT nextval('qc_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(14)}}))
			q := "CREATE TABLE qc_no_param AS SELECT 1/$1::integer AS x WITH NO DATA"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_no_param")
			for _, tc := range []struct {
				args    []any
				message string
			}{
				{nil, "sql: expected 1 arguments, got 0"},
				{[]any{1, 2}, "sql: expected 1 arguments, got 2"},
			} {
				result, err := stmt.ExecContext(p.Ctx, tc.args...)
				if result != nil || err == nil || err.Error() != tc.message {
					t.Fatalf("argument count result=%v err=%v want=%q", result, err, tc.message)
				}
				fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_no_param")
			}
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0, 0))
			run(fixture.Select("SELECT x FROM qc_no_param", []string{"x"}, []string{"INT4"}, nil))
			run(fixture.Exec("DROP TABLE qc_no_param", 0))
			invalid := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
			invalid.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, invalid)
			fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_no_param")
			run(fixture.Exec("CREATE TABLE qc_no_zero AS SELECT FROM qc_source WITH NO DATA", 0))
			run(fixture.Select("SELECT * FROM qc_no_zero", []string{}, []string{}, nil))
		})
	}
}

func TestReferenceQueryTableCreateSkip(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
			run(fixture.Exec("CREATE TABLE qc_existing(v TEXT)", 0))
			run(fixture.Exec("INSERT INTO qc_existing VALUES('sentinel')", 1))
			run(fixture.Exec("CREATE SEQUENCE qc_skip_seq START 10", 0))
			for _, body := range []string{"SELECT 1/0 AS x", "SELECT nextval('qc_skip_seq') AS n", "SELECT 'bad'::text::integer AS x"} {
				q := "CREATE TABLE IF NOT EXISTS qc_existing AS " + body
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				if len(notices) != 0 {
					t.Fatalf("Prepare emitted notices: %#v", notices)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, record, fixture.Exec(q, 0))
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "qc_existing" already exists, skipping`}
				if len(notices) != 1 || notices[0] != want {
					t.Fatalf("notices %#v want %#v", notices, want)
				}
				notices = nil
				run(fixture.Select("SELECT * FROM qc_existing", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
			}
			run(fixture.Select("SELECT nextval('qc_skip_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
		})
	}
}
