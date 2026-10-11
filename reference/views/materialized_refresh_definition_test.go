package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedRefreshDefinition(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_star AS SELECT * FROM mv_base", 3))
			run(fixture.Exec("CREATE VIEW refresh_dynamic AS SELECT i,s FROM mv_base", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_outer AS SELECT i,s FROM refresh_dynamic", 3))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_inner AS SELECT i,s FROM mv_base", 3))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_nested AS SELECT i,s FROM refresh_inner", 3))
			run(fixture.Exec("ALTER TABLE mv_base RENAME TO refresh_renamed", 0))
			run(fixture.Exec("ALTER TABLE refresh_renamed RENAME COLUMN i TO key", 0))
			run(fixture.Exec("ALTER TABLE refresh_renamed ADD COLUMN extra INT DEFAULT 9", 0))
			run(fixture.Exec("INSERT INTO refresh_renamed(key,s) VALUES(3,'c')", 1))
			four := [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}
			for _, name := range []string{"refresh_star", "refresh_outer"} {
				run(fixture.Exec("REFRESH MATERIALIZED VIEW "+name, 0))
				run(refreshBag(name, four))
			}
			run(fixture.Select("SELECT pg_get_viewdef('refresh_star'::regclass,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{" SELECT key AS i,\n    s\n   FROM refresh_renamed;"}}))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_nested", 0))
			run(materializedViewBag("refresh_nested"))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_inner", 0))
			run(materializedViewBag("refresh_nested"))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_nested", 0))
			run(refreshBag("refresh_nested", four))
			// A same-name shadow cannot rebind the frozen source.
			run(fixture.Exec("CREATE TABLE mv_base(i INT,s TEXT)", 0))
			run(fixture.Exec("INSERT INTO mv_base VALUES(99,'shadow')", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_star", 0))
			run(refreshBag("refresh_star", four))
			// Dropping an unreferenced leading slot must not shift frozen attribute bindings.
			run(fixture.Exec("CREATE TABLE refresh_layout(unused INT,i INT,s TEXT)", 0))
			run(fixture.Exec("INSERT INTO refresh_layout VALUES(99,7,'seven')", 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_bridge AS SELECT i,s FROM refresh_layout", 1))
			run(fixture.Exec("ALTER TABLE refresh_layout DROP COLUMN unused", 0))
			run(fixture.Exec("ALTER TABLE refresh_layout ADD COLUMN later INT DEFAULT 8", 0))
			run(fixture.Exec("INSERT INTO refresh_layout(i,s) VALUES(9,NULL)", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_bridge", 0))
			run(refreshBag("refresh_bridge", [][]any{{int64(7), "seven"}, {int64(9), nil}}))
			// REFRESH and MATERIALIZED retain unreserved identifier legality.
			run(fixture.Exec("CREATE TABLE refresh(materialized INT)", 0))
			run(fixture.Exec("INSERT INTO refresh VALUES(42)", 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW materialized AS SELECT materialized FROM refresh", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW materialized", 0))
			run(fixture.Select("SELECT materialized FROM materialized", []string{"materialized"}, []string{"INT4"}, [][]any{{int64(42)}}))
		})
	}
}

func TestReferenceMaterializedRefreshPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			q := "REFRESH MATERIALIZED VIEW refresh_later"
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("utility Describe must not resolve target", err)
			}
			defer held.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42P01", `relation "refresh_later" does not exist`)))
			run(fixture.Exec("CREATE TABLE refresh_source(i INT)", 0))
			run(fixture.Exec("INSERT INTO refresh_source VALUES(7)", 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_later AS SELECT i FROM refresh_source WITH NO DATA", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			run(fixture.Select("SELECT i FROM refresh_later", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("DROP MATERIALIZED VIEW refresh_later", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_later AS SELECT 9 AS i WITH NO DATA", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			run(fixture.Select("SELECT i FROM refresh_later", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
			run(fixture.Select(q, []string{}, []string{}, nil))
			want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "REFRESH"`)
			want.Position = "28"
			run(fixture.ErrorDiagnostic("PREPARE refresh_prepare AS REFRESH MATERIALIZED VIEW refresh_later", want))
		})
	}
}

func TestReferenceMaterializedRefreshDiagnostics(t *testing.T) {
	cases := []struct{ name, code, message string }{
		{"refresh_missing", "42P01", `relation "refresh_missing" does not exist`},
		{"refresh_table", "0A000", `"refresh_table" is not a materialized view`},
		{"refresh_view", "42809", `"refresh_view" is not a table or materialized view`},
		{"refresh_sequence", "42809", `"refresh_sequence" is not a table or materialized view`},
		{"refresh_index", "42809", `"refresh_index" is not a table or materialized view`},
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("CREATE TABLE refresh_table(i INT)", 0))
			run(fixture.Exec("CREATE VIEW refresh_view AS SELECT i FROM refresh_table", 0))
			run(fixture.Exec("CREATE SEQUENCE refresh_sequence", 0))
			run(fixture.Exec("CREATE INDEX refresh_index ON refresh_table(i)", 0))
			for _, tc := range cases {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW "+tc.name, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
			}
			run(fixture.Select("SELECT i FROM refresh_table", []string{"i"}, []string{"INT4"}, nil))
		})
	}
}
