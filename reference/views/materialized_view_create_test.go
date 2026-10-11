package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func materializedViewSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE mv_base(i INT,s TEXT)", 0))
	run(fixture.Exec("INSERT INTO mv_base VALUES(1,'a'),(1,'a'),(2,NULL)", 3))
}

func materializedViewBag(name string) fixture.Step {
	s := fixture.Select("SELECT i,s FROM "+name, []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}})
	s.Bag = true
	return s
}

func TestReferenceMaterializedViewCreate(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_stored AS SELECT i,s FROM mv_base", 3))
			run(fixture.Prepared(fixture.Exec("CREATE MATERIALIZED VIEW mv_explicit AS SELECT i,s FROM mv_base WITH DATA", 3)))
			run(fixture.Select("CREATE MATERIALIZED VIEW mv_cursor AS SELECT i,s FROM mv_base", []string{}, []string{}, nil))
			q := "SELECT i,s FROM mv_stored"
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(fixture.Exec("INSERT INTO mv_base VALUES(3,'b')", 1))
			run(fixture.Select("SELECT count(*) AS n FROM mv_base", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			for _, name := range []string{"mv_stored", "mv_explicit", "mv_cursor"} {
				run(materializedViewBag(name))
				run(fixture.Prepared(materializedViewBag(name)))
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, materializedViewBag("mv_stored"))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_prefix(k) AS SELECT i,s FROM mv_stored", 3))
			s := materializedViewBag("mv_prefix")
			s.Query, s.Columns = "SELECT k,s FROM mv_prefix", []string{"k", "s"}
			run(s)
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_values AS VALUES(1,'x'),(2,NULL)", 2))
			run(fixture.Select("SELECT * FROM mv_values ORDER BY column1", []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), nil}}))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_zero AS SELECT FROM mv_stored", 3))
			run(fixture.Select("SELECT * FROM mv_zero", []string{}, []string{}, [][]any{{}, {}, {}}))
			run(fixture.Exec(`CREATE MATERIALIZED VIEW "mv.quoted"(k) AS SELECT i FROM mv_stored`, 3))
			run(fixture.Select(`SELECT count(*) AS n FROM "mv.quoted"`, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
			fixture.MaterializedViewPopulation(run, "mv_stored", true)
		})
	}
}

func TestReferenceMaterializedViewPopulation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			q := "CREATE MATERIALIZED VIEW mv_unpopulated AS SELECT 1/0 AS x WITH NO DATA"
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			fixture.QueryTableCreateAbsent(t, run, b.PG, "mv_unpopulated")
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			fixture.MaterializedViewPopulation(run, "mv_unpopulated", false)
			run(fixture.MaterializedViewUnpopulated("mv_unpopulated"))
			run(fixture.Prepared(fixture.MaterializedViewUnpopulated("mv_unpopulated")))
			run(fixture.Exec("CREATE VIEW mv_unpopulated_view AS SELECT * FROM mv_unpopulated", 0))
			s := fixture.MaterializedViewUnpopulated("mv_unpopulated")
			s.Query = "SELECT * FROM mv_unpopulated_view"
			run(s)
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_empty AS SELECT i,s FROM mv_base WHERE false WITH DATA", 0))
			fixture.MaterializedViewPopulation(run, "mv_empty", true)
			empty := fixture.Select("SELECT i,s FROM mv_empty", []string{"i", "s"}, []string{"INT4", "TEXT"}, nil)
			empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, cols, empty.Columns, empty.Types, []bool{true, true})
			}
			run(fixture.Prepared(empty))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_existing AS SELECT i,s FROM mv_base", 3))
			var notices []fixture.Diagnostic
			_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("CREATE MATERIALIZED VIEW IF NOT EXISTS mv_existing AS SELECT 1/0 AS x", 0))
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "mv_existing" already exists, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
			run(materializedViewBag("mv_existing"))
			run(fixture.Select("SELECT pg_get_viewdef('mv_existing'::regclass,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{" SELECT i,\n    s\n   FROM mv_base;"}}))
		})
	}
}
