package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func alterBag(name string, rows [][]any) fixture.Step {
	s := fixture.Select("SELECT key,s FROM "+name, []string{"key", "s"}, []string{"INT4", "TEXT"}, rows)
	s.Bag = true
	return s
}

var alterThree = [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}}
var alterFour = [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}

func alterSeed(run func(fixture.Step)) {
	materializedViewSeed(run)
	run(fixture.Exec("CREATE MATERIALIZED VIEW alter_mv AS SELECT i,s FROM mv_base", 3))
}
func alterIdentity(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) [3]int64 {
	t.Helper()
	var ids [3]int64
	q := "SELECT c.oid,'" + name + "'::regtype::oid,w.oid FROM pg_class c JOIN pg_rewrite w ON w.ev_class=c.oid WHERE c.oid='" + name + "'::regclass::oid"
	if err := c.QueryRowContext(p.Ctx, q).Scan(&ids[0], &ids[1], &ids[2]); err != nil {
		t.Fatal(err)
	}
	if ids[0] == 0 || ids[1] == 0 || ids[2] == 0 {
		t.Fatal("zero catalog identity", ids)
	}
	return ids
}
func TestReferenceMaterializedAlterIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "alter_s2")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			alterSeed(run)
			run(fixture.Exec("CREATE VIEW alter_dependent AS SELECT i,s FROM alter_mv", 0))
			ids := alterIdentity(t, p, c, "alter_mv")
			run(fixture.Exec("INSERT INTO mv_base VALUES(3,'c')", 1))
			var retained *sql.Rows
			if !b.PG {
				var err error
				retained, err = p.Native.QueryContext(p.Ctx, "SELECT i,s FROM alter_mv ORDER BY i")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
			}
			run(fixture.Prepared(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key", 0)))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME TO saved", 0))
			run(fixture.Exec("CREATE SCHEMA alter_s2", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW saved SET SCHEMA alter_s2", 0))
			if got := alterIdentity(t, p, c, "alter_s2.saved"); got != ids {
				t.Fatalf("identity changed: %v -> %v", ids, got)
			}
			run(alterBag("alter_s2.saved", alterThree))
			run(fixture.Prepared(alterBag("alter_s2.saved", alterThree)))
			run(materializedViewBag("alter_dependent"))
			// Physical attnums are a PostgreSQL complement; shared direct/prepared bags
			// above independently assert exposed names, types and stored values.
			if b.PG {
				run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='alter_s2.saved'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"key", int64(1)}, {"s", int64(2)}}))
			}
			missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "alter_mv" does not exist`)
			missing.Position = "15"
			run(fixture.ErrorDiagnostic("SELECT * FROM alter_mv", missing))
			if retained != nil {
				materializedViewRetainedBag(t, retained)
			}
		})
	}
}
func TestReferenceMaterializedAlterDefinition(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "alter_source")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			alterSeed(run)
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME i TO key", 0))
			run(fixture.Exec("ALTER TABLE mv_base RENAME TO producer", 0))
			run(fixture.Exec("ALTER TABLE producer RENAME COLUMN i TO source_key", 0))
			run(fixture.Exec("CREATE SCHEMA alter_source", 0))
			run(fixture.Exec("ALTER TABLE producer SET SCHEMA alter_source", 0))
			run(fixture.Exec("CREATE TABLE mv_base(i INT,s TEXT)", 0))
			run(fixture.Exec("INSERT INTO mv_base VALUES(99,'shadow')", 1))
			run(fixture.Exec("INSERT INTO alter_source.producer VALUES(3,'c')", 1))
			run(alterBag("alter_mv", alterThree))
			for range 2 {
				run(fixture.Exec("REFRESH MATERIALIZED VIEW alter_mv", 0))
				run(alterBag("alter_mv", alterFour))
			}
			run(fixture.Select("SELECT pg_get_viewdef('alter_mv'::regclass,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{" SELECT source_key AS key,\n    s\n   FROM alter_source.producer;"}}))
		})
	}
}
func TestReferenceMaterializedAlterPopulation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			alterSeed(run)
			run(fixture.Exec("REFRESH MATERIALIZED VIEW alter_mv WITH NO DATA", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME TO saved", 0))
			fixture.MaterializedViewPopulation(run, "saved", false)
			run(fixture.MaterializedViewUnpopulated("saved"))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW saved", 0))
			run(alterBag("saved", alterThree))
			run(fixture.Exec("DELETE FROM mv_base", 3))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW saved", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW saved RENAME TO empty_saved", 0))
			fixture.MaterializedViewPopulation(run, "empty_saved", true)
			run(alterBag("empty_saved", nil))
		})
	}
}
func TestReferenceMaterializedAlterMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("CREATE MATERIALIZED VIEW typed AS SELECT 'abcd'::varchar(4) AS v,12.34::numeric(5,2) AS n", 1))
			run(fixture.Exec("ALTER MATERIALIZED VIEW typed RENAME COLUMN v TO label", 0))
			s := fixture.Select("SELECT * FROM typed", []string{"label", "n"}, []string{"VARCHAR", "NUMERIC"}, [][]any{{"abcd", "12.34"}})
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				cols, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, cols, s.Columns, s.Types, []bool{true, true})
				l, ok := cols[0].Length()
				if !ok || l != 4 {
					t.Fatalf("length %d/%t", l, ok)
				}
				p, s, ok := cols[1].DecimalSize()
				if !ok || p != 5 || s != 2 {
					t.Fatalf("numeric %d/%d/%t", p, s, ok)
				}
			}
			run(s)
			run(fixture.Prepared(s))
			var owner string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT schemaname,matviewname,matviewowner,hasindexes,ispopulated FROM pg_matviews WHERE matviewname='typed'", []string{"schemaname", "matviewname", "matviewowner", "hasindexes", "ispopulated"}, []string{"NAME", "NAME", "NAME", "BOOL", "BOOL"}, [][]any{{"public", "typed", owner, false, true}}))
		})
	}
}
