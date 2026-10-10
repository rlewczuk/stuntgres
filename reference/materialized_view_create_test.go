package reference_test

import (
	"database/sql"
	"testing"
)

func materializedViewSeed(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE mv_base(i INT,s TEXT)", 0))
	run(referenceExec("INSERT INTO mv_base VALUES(1,'a'),(1,'a'),(2,NULL)", 3))
}

func materializedViewBag(name string) referenceStep {
	s := referenceSelect("SELECT i,s FROM "+name, []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}})
	s.bag = true
	return s
}

func materializedViewUnpopulated(name string) referenceStep {
	want := controlsReferenceDiagnostic("55000", `materialized view "`+name+`" has not been populated`)
	want.Hint = "Use the REFRESH MATERIALIZED VIEW command."
	s := referenceErrorDiagnostic("SELECT * FROM "+name, want)
	s.selectRows = true
	s.errorStage = "execute"
	return s
}

func materializedViewPopulation(run func(referenceStep), name string, populated bool) {
	run(referenceSelect("SELECT relkind='m' AS m,relhasrules,relispopulated FROM pg_class WHERE relname='"+name+"'", []string{"m", "relhasrules", "relispopulated"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, populated}}))
}

func TestReferenceMaterializedViewCreate(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(referenceExec("CREATE MATERIALIZED VIEW mv_stored AS SELECT i,s FROM mv_base", 3))
			run(referencePrepared(referenceExec("CREATE MATERIALIZED VIEW mv_explicit AS SELECT i,s FROM mv_base WITH DATA", 3)))
			run(referenceSelect("CREATE MATERIALIZED VIEW mv_cursor AS SELECT i,s FROM mv_base", []string{}, []string{}, nil))
			q := "SELECT i,s FROM mv_stored"
			held, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(referenceExec("INSERT INTO mv_base VALUES(3,'b')", 1))
			run(referenceSelect("SELECT count(*) AS n FROM mv_base", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			for _, name := range []string{"mv_stored", "mv_explicit", "mv_cursor"} {
				run(materializedViewBag(name))
				run(referencePrepared(materializedViewBag(name)))
			}
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: held, query: q}, b.recorder, materializedViewBag("mv_stored"))
			run(referenceExec("CREATE MATERIALIZED VIEW mv_prefix(k) AS SELECT i,s FROM mv_stored", 3))
			s := materializedViewBag("mv_prefix")
			s.query, s.columns = "SELECT k,s FROM mv_prefix", []string{"k", "s"}
			run(s)
			run(referenceExec("CREATE MATERIALIZED VIEW mv_values AS VALUES(1,'x'),(2,NULL)", 2))
			run(referenceSelect("SELECT * FROM mv_values ORDER BY column1", []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), nil}}))
			run(referenceExec("CREATE MATERIALIZED VIEW mv_zero AS SELECT FROM mv_stored", 3))
			run(referenceSelect("SELECT * FROM mv_zero", []string{}, []string{}, [][]any{{}, {}, {}}))
			run(referenceExec(`CREATE MATERIALIZED VIEW "mv.quoted"(k) AS SELECT i FROM mv_stored`, 3))
			run(referenceSelect(`SELECT count(*) AS n FROM "mv.quoted"`, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
			materializedViewPopulation(run, "mv_stored", true)
		})
	}
}

func TestReferenceMaterializedViewPopulation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			q := "CREATE MATERIALIZED VIEW mv_unpopulated AS SELECT 1/0 AS x WITH NO DATA"
			held, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			queryTableCreateAbsent(t, run, b.pg, "mv_unpopulated")
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: held, query: q}, b.recorder, referenceExec(q, 0))
			materializedViewPopulation(run, "mv_unpopulated", false)
			run(materializedViewUnpopulated("mv_unpopulated"))
			run(referencePrepared(materializedViewUnpopulated("mv_unpopulated")))
			run(referenceExec("CREATE VIEW mv_unpopulated_view AS SELECT * FROM mv_unpopulated", 0))
			s := materializedViewUnpopulated("mv_unpopulated")
			s.query = "SELECT * FROM mv_unpopulated_view"
			run(s)
			run(referenceExec("CREATE MATERIALIZED VIEW mv_empty AS SELECT i,s FROM mv_base WHERE false WITH DATA", 0))
			materializedViewPopulation(run, "mv_empty", true)
			empty := referenceSelect("SELECT i,s FROM mv_empty", []string{"i", "s"}, []string{"INT4", "TEXT"}, nil)
			empty.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				referenceTableDDLColumnTypes(t, b.pg, cols, empty.columns, empty.types, []bool{true, true})
			}
			run(referencePrepared(empty))
			run(referenceExec("CREATE MATERIALIZED VIEW mv_existing AS SELECT i,s FROM mv_base", 3))
			var notices []referenceDiagnostic
			_, noticeConn, recorder := noticeReferencePool(t, p, b.pg, &notices)
			referenceRunStep(t, p.ctx, noticeConn, recorder, referenceExec("CREATE MATERIALIZED VIEW IF NOT EXISTS mv_existing AS SELECT 1/0 AS x", 0))
			want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "mv_existing" already exists, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
			run(materializedViewBag("mv_existing"))
			run(referenceSelect("SELECT pg_get_viewdef('mv_existing'::regclass,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{" SELECT i,\n    s\n   FROM mv_base;"}}))
		})
	}
}
