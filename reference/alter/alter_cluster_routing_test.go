package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func clusterRouting(t *testing.T) {
	t.Helper()
	t.Run("conditional-and-materialized", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				var notices []fixture.Diagnostic
				_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				b.Recorder = rec
				clusterSetup(t, p, b, c)
				persistenceRun(t, p, b, c, fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT id FROM t", 2))
				persistenceExec(t, p, b, c, "CREATE INDEX mi ON mv(id)")
				for _, prepared := range []bool{false, true} {
					for _, action := range []string{"CLUSTER ON a", "SET WITHOUT CLUSTER", "SET WITHOUT OIDS"} {
						notices = nil
						step := fixture.Exec("ALTER TABLE IF EXISTS t "+action, 0)
						if prepared {
							step = fixture.Prepared(step)
						}
						persistenceRun(t, p, b, c, step)
						if len(notices) != 0 {
							t.Fatal(notices)
						}
						notices = nil
						step = fixture.Exec("ALTER TABLE IF EXISTS absent_cluster "+action, 0)
						if prepared {
							step = fixture.Prepared(step)
						}
						persistenceRun(t, p, b, c, step)
						want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "absent_cluster" does not exist, skipping`}
						if len(notices) != 1 || notices[0] != want {
							t.Fatal(notices, want)
						}
					}
					for _, prefix := range []string{"ALTER MATERIALIZED VIEW mv ", "ALTER MATERIALIZED VIEW IF EXISTS mv "} {
						for _, action := range []string{"CLUSTER ON mi", "SET WITHOUT CLUSTER"} {
							step := fixture.Exec(prefix+action, 0)
							if prepared {
								step = fixture.Prepared(step)
							}
							persistenceRun(t, p, b, c, step)
							persistenceRun(t, p, b, c, clusterState("mv", [][]any{{"mi", action == "CLUSTER ON mi"}}))
						}
					}
				}
			})
		}
	})
	t.Run("mixed-inherited-late-failure", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE TABLE parent(id int,CONSTRAINT keep CHECK(id>0))",
				"CREATE TABLE child(CONSTRAINT child_keep CHECK(id<10)) INHERITS(parent)",
				"CREATE INDEX pa ON parent(id)",
				"CREATE INDEX pb ON parent(id DESC)",
				"CREATE INDEX ch ON child(id)",
				"CREATE INDEX cx ON child(id DESC)",
				"ALTER TABLE parent CLUSTER ON pb",
				"ALTER TABLE child CLUSTER ON ch",
			} {
				persistenceExec(t, p, b, c, q)
			}
			persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO parent VALUES(1)", 1))
			persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO child VALUES(4)", 1))
			check := func() {
				t.Helper()
				for _, table := range []string{"parent", "child"} {
					local, inherited := table == "parent", int64(0)
					if !local {
						inherited = 1
					}
					persistenceRun(t, p, b, c, fixture.Select("SELECT attname,attnum,attislocal,attinhcount,atthasdef FROM pg_attribute WHERE attrelid='"+table+"'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "attislocal", "attinhcount", "atthasdef"}, []string{"NAME", "INT2", "BOOL", "INT2", "BOOL"}, [][]any{{"id", int64(1), local, inherited, false}}))
					constraints := [][]any{{"keep", "c", true, local, inherited}}
					if table == "child" {
						constraints = append([][]any{{"child_keep", "c", true, true, int64(0)}}, constraints...)
					}
					persistenceRun(t, p, b, c, fixture.Select("SELECT conname,contype,convalidated,conislocal,coninhcount FROM pg_constraint WHERE conrelid='"+table+"'::regclass ORDER BY conname", []string{"conname", "contype", "convalidated", "conislocal", "coninhcount"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "INT2"}, constraints))
				}
				persistenceRun(t, p, b, c, fixture.Select("SELECT * FROM ONLY parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				persistenceRun(t, p, b, c, fixture.Select("SELECT * FROM ONLY child ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}))
				persistenceRun(t, p, b, c, clusterState("parent", [][]any{{"pa", false}, {"pb", true}}))
				persistenceRun(t, p, b, c, clusterState("child", [][]any{{"ch", true}, {"cx", false}}))
			}
			check()
			want := persistenceDiagnostic("23514", `check constraint "reject" of relation "child" is violated by some row`)
			want.Schema, want.Table, want.Constraint = "public", "child", "reject"
			for _, prepared := range []bool{false, true} {
				// The root row passes; only descendant validation fails after the
				// recursive column work and root-only metadata actions are staged.
				persistenceFailure(t, p, b, c, "ALTER TABLE parent ADD COLUMN extra int DEFAULT 9, CLUSTER ON pa, SET WITHOUT OIDS, ADD CONSTRAINT reject CHECK(id<3)", prepared, want)
				check()
			}
		})
	})
	t.Run("mixed-inherited-shape", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE TABLE parent(id int)", "CREATE TABLE child() INHERITS(parent)", "CREATE INDEX pa ON parent(id)", "CREATE INDEX ch ON child(id)", "ALTER TABLE child CLUSTER ON ch"} {
				persistenceExec(t, p, b, c, q)
			}
			persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO child VALUES(4)", 1))
			persistenceExec(t, p, b, c, "ALTER TABLE parent ADD COLUMN extra int DEFAULT 9, CLUSTER ON pa, SET WITHOUT OIDS")
			persistenceRun(t, p, b, c, fixture.Select("SELECT id,extra FROM child ORDER BY id", []string{"id", "extra"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(9)}}))
			persistenceRun(t, p, b, c, clusterState("parent", [][]any{{"pa", true}}))
			persistenceRun(t, p, b, c, clusterState("child", [][]any{{"ch", true}}))
		})
	})
}
