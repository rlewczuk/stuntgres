package reference_test

import "testing"

func TestReferenceQueryTableCreateNoData(t *testing.T) {
	for _, body := range []string{"SELECT 1/0 AS x", "SELECT 'bad'::text::integer AS x", "SELECT (SELECT 1/0) AS x"} {
		t.Run(body, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					_, run := queryTableCreateSession(t, p, b)
					run(referenceExec("CREATE TABLE qc_nodata AS "+body+" WITH NO DATA", 0))
					run(referenceSelect("SELECT x FROM qc_nodata", []string{"x"}, []string{"INT4"}, nil))
				})
			}
		})
	}
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			queryTableCreateSource(run)
			run(referenceExec("CREATE SEQUENCE qc_draw START 10", 0))
			run(referencePrepared(referenceExec("CREATE TABLE qc_no_draw AS SELECT nextval('qc_draw') AS n FROM qc_source WITH NO DATA", 0)))
			run(referenceSelect("SELECT nextval('qc_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
			run(referenceExec("CREATE TABLE qc_drawn AS SELECT nextval('qc_draw') AS n FROM qc_source", 3))
			run(referenceSelect("SELECT n FROM qc_drawn ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(11)}, {int64(12)}, {int64(13)}}))
			run(referenceSelect("SELECT nextval('qc_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(14)}}))
			q := "CREATE TABLE qc_no_param AS SELECT 1/$1::integer AS x WITH NO DATA"
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			queryTableCreateAbsent(t, run, b.pg, "qc_no_param")
			for _, tc := range []struct {
				args    []any
				message string
			}{
				{nil, "sql: expected 1 arguments, got 0"},
				{[]any{1, 2}, "sql: expected 1 arguments, got 2"},
			} {
				result, err := stmt.ExecContext(p.ctx, tc.args...)
				if result != nil || err == nil || err.Error() != tc.message {
					t.Fatalf("argument count result=%v err=%v want=%q", result, err, tc.message)
				}
				queryTableCreateAbsent(t, run, b.pg, "qc_no_param")
			}
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 0, 0))
			run(referenceSelect("SELECT x FROM qc_no_param", []string{"x"}, []string{"INT4"}, nil))
			run(referenceExec("DROP TABLE qc_no_param", 0))
			invalid := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
			invalid.errorStage = "execute"
			referenceRunStep(t, p.ctx, held, b.recorder, invalid)
			queryTableCreateAbsent(t, run, b.pg, "qc_no_param")
			run(referenceExec("CREATE TABLE qc_no_zero AS SELECT FROM qc_source WITH NO DATA", 0))
			run(referenceSelect("SELECT * FROM qc_no_zero", []string{}, []string{}, nil))
		})
	}
}

func TestReferenceQueryTableCreateSkip(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, record := noticeReferencePool(t, p, b.pg, &notices)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) }
			run(referenceExec("CREATE TABLE qc_existing(v TEXT)", 0))
			run(referenceExec("INSERT INTO qc_existing VALUES('sentinel')", 1))
			run(referenceExec("CREATE SEQUENCE qc_skip_seq START 10", 0))
			for _, body := range []string{"SELECT 1/0 AS x", "SELECT nextval('qc_skip_seq') AS n", "SELECT 'bad'::text::integer AS x"} {
				q := "CREATE TABLE IF NOT EXISTS qc_existing AS " + body
				stmt, err := conn.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				if len(notices) != 0 {
					t.Fatalf("Prepare emitted notices: %#v", notices)
				}
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}, record, referenceExec(q, 0))
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "qc_existing" already exists, skipping`}
				if len(notices) != 1 || notices[0] != want {
					t.Fatalf("notices %#v want %#v", notices, want)
				}
				notices = nil
				run(referenceSelect("SELECT * FROM qc_existing", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
			}
			run(referenceSelect("SELECT nextval('qc_skip_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
		})
	}
}
