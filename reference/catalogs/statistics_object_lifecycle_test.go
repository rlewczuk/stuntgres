package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsObjectDrop(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT,c INT)", 0))
			run(fixture.Exec("INSERT INTO stats_t VALUES(1,2,3)", 1))
			run(fixture.Exec("CREATE STATISTICS stats_s (mcv,dependencies) ON b,a FROM stats_t", 0))
			oid := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='stats_s'")
			for _, prepared := range []bool{false, true} {
				q := fixture.Exec("CREATE STATISTICS IF NOT EXISTS stats_s (bogus) ON missing FROM stats_t", 0)
				if prepared {
					q = fixture.Prepared(q)
				}
				notices = nil
				run(q)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `statistics object "stats_s" already exists, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("conditional create notice: %#v", notices)
				}
				if got := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='stats_s'"); got != oid {
					t.Fatal("conditional create replaced identity", oid, got)
				}
				run(statisticsRows([]any{"stats_s", "1 2", "{f,m}", nil, nil}))
				notices = nil
				q = fixture.Exec("DROP STATISTICS IF EXISTS missing_stats", 0)
				if prepared {
					q = fixture.Prepared(q)
				}
				run(q)
				want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `statistics object "missing_stats" does not exist, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("conditional drop notice: %#v", notices)
				}
			}
			notices = nil
			run(fixture.Exec("DROP STATISTICS IF EXISTS missing_schema.absent", 0))
			if want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "missing_schema" does not exist, skipping`}}; !reflect.DeepEqual(notices, want) {
				t.Fatal(notices)
			}
			run(statisticsError("DROP STATISTICS stats_s,absent", "42704", `statistics object "absent" does not exist`))
			run(statisticsRows([]any{"stats_s", "1 2", "{f,m}", nil, nil}))
			run(fixture.Exec("DROP STATISTICS stats_s CASCADE", 0))
			run(statisticsRows())
			run(fixture.Select("SELECT * FROM stats_t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3)}}))
		})
	}
}

func TestReferenceStatisticsObjectOwnership(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"stats_owner", "stats_other_owner"})
	fixture.SchemaReferenceCleanup(t, "stats_owned_schema")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET ROLE")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE stats_owner", "CREATE ROLE stats_other_owner", "CREATE TABLE stats_t(a INT,b INT)", "GRANT SELECT ON stats_t TO stats_owner", "GRANT USAGE,CREATE ON SCHEMA public TO stats_owner", "CREATE STATISTICS stats_s ON a,b FROM stats_t", "SET ROLE stats_owner"} {
				run(fixture.Exec(q, 0))
			}
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ q, msg string }{{"CREATE STATISTICS forbidden ON a,b FROM stats_t", "must be owner of table stats_t"}, {"CREATE STATISTICS IF NOT EXISTS stats_s ON a,b FROM stats_t", "must be owner of table stats_t"}, {"DROP STATISTICS stats_s", "must be owner of statistics object stats_s"}} {
					s := statisticsError(tc.q, "42501", tc.msg)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
				}
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("ALTER TABLE stats_t OWNER TO stats_owner", 0))
			run(fixture.Exec("SET ROLE stats_owner", 0))
			run(fixture.Exec("CREATE STATISTICS owned_s ON b,a FROM stats_t", 0))
			run(statisticsError("DROP STATISTICS owned_s,stats_s", "42501", "must be owner of statistics object stats_s"))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Select("SELECT s.stxowner=r.oid AS owner_retained FROM pg_statistic_ext s JOIN pg_roles r ON r.rolname='stats_owner' WHERE s.stxname='owned_s'", []string{"owner_retained"}, []string{"BOOL"}, [][]any{{true}}))
			run(fixture.Exec("ALTER TABLE stats_t OWNER TO stats_other_owner", 0))
			run(fixture.Select("SELECT s.stxowner=r.oid AS owner_retained FROM pg_statistic_ext s JOIN pg_roles r ON r.rolname='stats_owner' WHERE s.stxname='owned_s'", []string{"owner_retained"}, []string{"BOOL"}, [][]any{{true}}))
			// The table no longer owns stats_owner's role dependency.
			run(fixture.Error("DROP ROLE stats_owner", "2BP01"))
			run(fixture.Exec("SET ROLE stats_owner", 0))
			run(fixture.Exec("DROP STATISTICS owned_s", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("CREATE SCHEMA stats_owned_schema AUTHORIZATION stats_owner", 0))
			run(fixture.Exec("CREATE STATISTICS stats_owned_schema.schema_s ON a,b FROM stats_t", 0))
			run(fixture.Exec("SET ROLE stats_owner", 0))
			run(fixture.Exec("DROP STATISTICS stats_owned_schema.schema_s", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("REVOKE CREATE ON SCHEMA public FROM stats_owner", 0))
			run(fixture.Exec("ALTER TABLE stats_t OWNER TO stats_owner", 0))
			run(fixture.Exec("SET ROLE stats_owner", 0))
			run(statisticsError("CREATE STATISTICS denied_schema ON a,b FROM stats_t", "42501", "permission denied for schema public"))
			run(fixture.Exec("RESET ROLE", 0))
		})
	}
}

func TestReferenceStatisticsObjectDependencies(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{"CREATE TABLE stats_t(a INT,b INT,c INT)", "CREATE STATISTICS stats_s ON b,a FROM stats_t"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Select("SELECT d.classid::bigint AS classid,d.objsubid,d.refclassid::bigint AS refclassid,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_statistic_ext s ON s.oid=d.objid WHERE s.stxname='stats_s' AND d.classid=3381 ORDER BY d.refclassid,d.refobjsubid", []string{"classid", "objsubid", "refclassid", "refobjsubid", "deptype"}, []string{"INT8", "INT4", "INT8", "INT4", "CHAR"}, [][]any{{int64(3381), int64(0), int64(1259), int64(1), "a"}, {int64(3381), int64(0), int64(1259), int64(2), "a"}, {int64(3381), int64(0), int64(2615), int64(0), "n"}}))
			run(fixture.Exec("ALTER TABLE stats_t DROP COLUMN c RESTRICT", 0))
			run(statisticsRows([]any{"stats_s", "1 2", "{d,f,m}", nil, nil}))
			run(fixture.Exec("ALTER TABLE stats_t DROP COLUMN a RESTRICT", 0))
			run(statisticsRows())
			run(fixture.Exec("ALTER TABLE stats_t ADD COLUMN replacement INT", 0))
			run(fixture.Exec("CREATE STATISTICS sparse_s ON b,replacement FROM stats_t", 0))
			run(statisticsRows([]any{"sparse_s", "2 4", "{d,f,m}", nil, nil}))
			run(fixture.Exec("ALTER TABLE stats_t RENAME COLUMN b TO renamed_b", 0))
			run(statisticsRows([]any{"sparse_s", "2 4", "{d,f,m}", nil, nil}))
			run(fixture.Exec("DROP TABLE stats_t RESTRICT", 0))
			run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname='sparse_s'", []string{"stxname"}, []string{"NAME"}, nil))
		})
	}
}

func TestReferenceStatisticsObjectTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("CREATE STATISTICS stats_s ON a,b FROM stats_t", 0))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, statisticsRows([]any{"stats_s", "1 2", "{d,f,m}", nil, nil}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, statisticsRows())
			for _, commit := range []bool{true, false} {
				tx, err = b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				q := "CREATE STATISTICS stats_s ON a,b FROM stats_t"
				if !commit {
					q = "DROP STATISTICS stats_s"
				}
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(q, 0))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, statisticsRows([]any{"stats_s", "1 2", "{d,f,m}", nil, nil}))
			}
			tx, err = b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DROP STATISTICS stats_s", 0))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, statisticsRows())
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, statisticsRows())
		})
	}
}

func TestReferenceStatisticsObjectFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"statistics_smoke_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT,c INT)", 0))
			run(fixture.Exec("INSERT INTO stats_t VALUES(1,2,3)", 1))
			for i, prepared := range []bool{false, true} {
				s := fixture.Exec(fmt.Sprintf("CREATE STATISTICS smoke_s%d (mcv,dependencies) ON b,a FROM stats_t", i), 0)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
			}
			run(statisticsRows([]any{"smoke_s0", "1 2", "{f,m}", nil, nil}, []any{"smoke_s1", "1 2", "{f,m}", nil, nil}))
			run(statisticsError("CREATE STATISTICS smoke_s0 ON a,b FROM stats_t", "42710", `statistics object "smoke_s0" already exists`))
			notices = nil
			run(fixture.Exec("CREATE STATISTICS IF NOT EXISTS smoke_s0 (bogus) ON missing FROM stats_t", 0))
			if want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `statistics object "smoke_s0" already exists, skipping`}}; !reflect.DeepEqual(notices, want) {
				t.Fatal(notices)
			}
			run(statisticsError("CREATE STATISTICS bad ON a,a FROM stats_t", "42701", "duplicate column name in statistics definition"))
			run(statisticsError("CREATE STATISTICS bad (bogus) ON a,b FROM stats_t", "42601", `unrecognized statistics kind "bogus"`))
			run(statisticsError("CREATE STATISTICS bad ON a FROM stats_t", "42P17", "extended statistics require at least 2 columns"))
			run(fixture.Exec("CREATE STATISTICS ON b,a FROM stats_t", 0))
			run(statisticsRows([]any{"smoke_s0", "1 2", "{f,m}", nil, nil}, []any{"smoke_s1", "1 2", "{f,m}", nil, nil}, []any{"stats_t_b_a_stat", "1 2", "{d,f,m}", nil, nil}))
			run(fixture.Exec("CREATE ROLE statistics_smoke_reader", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO statistics_smoke_reader", 0))
			run(fixture.Exec("SET ROLE statistics_smoke_reader", 0))
			run(statisticsError("DROP STATISTICS smoke_s0", "42501", "must be owner of statistics object smoke_s0"))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("DROP STATISTICS stats_t_b_a_stat CASCADE", 0))
			if !b.PG {
				var saved, again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.Len() == 0 {
					t.Fatal("empty feature dump")
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(saved.Bytes(), again.Bytes()) {
					t.Fatal("smoke roundtrip changed")
				}
			}
			run(fixture.Exec("ALTER TABLE stats_t DROP COLUMN c RESTRICT", 0))
			run(statisticsRows([]any{"smoke_s0", "1 2", "{f,m}", nil, nil}, []any{"smoke_s1", "1 2", "{f,m}", nil, nil}))
			run(fixture.Exec("ALTER TABLE stats_t DROP COLUMN a RESTRICT", 0))
			run(statisticsRows())
			run(fixture.Select("SELECT b FROM stats_t", []string{"b"}, []string{"INT4"}, [][]any{{int64(2)}}))
			run(fixture.Select("SELECT NULL::pg_ndistinct AS n,NULL::pg_dependencies AS d,NULL::pg_mcv_list AS m", []string{"n", "d", "m"}, []string{statisticsDatabaseTypeName(b.PG, "pg_ndistinct"), statisticsDatabaseTypeName(b.PG, "pg_dependencies"), statisticsDatabaseTypeName(b.PG, "pg_mcv_list")}, [][]any{{nil, nil, nil}}))
			notices = nil
			run(fixture.Prepared(fixture.Exec("DROP STATISTICS IF EXISTS smoke_s0", 0)))
			if want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `statistics object "smoke_s0" does not exist, skipping`}}; !reflect.DeepEqual(notices, want) {
				t.Fatal(notices)
			}
			for _, name := range []string{"pg_ndistinct", "pg_dependencies", "pg_mcv_list"} {
				want := fixture.ControlsReferenceDiagnostic("0A000", "cannot accept a value of type "+name)
				want.Position = "8"
				s := fixture.ErrorDiagnostic("SELECT ''::"+name, want)
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
			}
		})
	}
}
