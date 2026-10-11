package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceIndexPropertiesConsumers(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Select("SELECT a FROM ip_data WHERE pg_index_column_has_property('ip_mixed',a,'desc') IS TRUE ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}),
			fixture.Select("SELECT x.a FROM ip_data x JOIN ip_data y ON x.a=y.a AND pg_index_has_property('ip_default','index_scan') IS TRUE ORDER BY x.a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			indexPropertiesBoolStep("SELECT CASE WHEN pg_index_column_has_property('ip_mixed',5,'asc') IS NULL THEN pg_index_has_property('ip_default','index_scan') ELSE false END AS result FROM ip_data WHERE a=1", []string{"result"}, true),
			indexPropertiesBoolStep("SELECT COALESCE(pg_index_column_has_property('ip_mixed',5,'asc'),false) AS result FROM ip_data WHERE a=1", []string{"result"}, false),
			fixture.Select("SELECT pg_index_column_has_property('ip_mixed',a,'desc') AS result,count(*) AS n FROM ip_data GROUP BY pg_index_column_has_property('ip_mixed',a,'desc') ORDER BY result", []string{"result", "n"}, []string{"BOOL", "INT8"}, [][]any{{false, int64(1)}, {true, int64(1)}}),
			indexPropertiesBoolStep("SELECT pg_index_column_has_property('ip_mixed',max(a),'desc') AS result FROM ip_data", []string{"result"}, true),
			fixture.Select("SELECT count(pg_index_column_has_property('ip_mixed',a,'asc')) AS n FROM ip_data", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
			fixture.Exec("CREATE VIEW ip_property_view AS SELECT a,pg_index_column_has_property('ip_mixed',a,'desc') AS result FROM ip_data", 0),
			fixture.Select("SELECT result FROM ip_property_view ORDER BY a", []string{"result"}, []string{"BOOL"}, [][]any{{false}, {true}}),
			fixture.Exec("CREATE TABLE ip_checked(n INT CHECK(pg_index_column_has_property('ip_mixed',n,'orderable') IS TRUE))", 0),
			fixture.Exec("INSERT INTO ip_checked VALUES(1)", 1),
			fixture.Exec("UPDATE ip_checked SET n=2", 1),
			fixture.Select("SELECT n FROM ip_checked", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}),
		} {
			run(s)
		}
		w := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "ip_checked" violates check constraint "ip_checked_n_check"`)
		w.Detail = "Failing row contains (5)."
		w.Schema = "public"
		w.Table = "ip_checked"
		w.Constraint = "ip_checked_n_check"
		run(fixture.ErrorDiagnostic("INSERT INTO ip_checked VALUES(5)", w))
		limitQuery := "SELECT a FROM ip_data LIMIT CASE WHEN pg_index_has_property(a,'index_scan') THEN 1 ELSE 0 END"
		w = fixture.ControlsReferenceDiagnostic("42P10", "argument of LIMIT must not contain variables")
		w.Position = fixture.Position(strings.Index(limitQuery, "(a,") + 2)
		run(fixture.ErrorDiagnostic(limitQuery, w))
		w = fixture.ControlsReferenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE")
		run(fixture.ErrorDiagnostic("CREATE INDEX ip_stable ON ip_data((pg_index_has_property('ip_default','index_scan')))", w))
		w = fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable")
		run(fixture.ErrorDiagnostic("CREATE TABLE ip_generated(a BOOL GENERATED ALWAYS AS (pg_indexam_has_property(403,'can_order')) STORED)", w))
		// The native DEFAULT boundary intentionally differs from PostgreSQL admission.
		q := "CREATE TABLE ip_default_boundary(a BOOL DEFAULT pg_indexam_has_property(403,'can_order'))"
		if b.PG {
			run(fixture.Exec(q, 0))
			run(fixture.Exec("INSERT INTO ip_default_boundary DEFAULT VALUES", 1))
			run(indexPropertiesBoolStep("SELECT a FROM ip_default_boundary", []string{"a"}, true))
		} else {
			w = fixture.ControlsReferenceDiagnostic("0A000", "functions are unsupported")
			w.Position = fixture.Position(strings.Index(q, "pg_indexam") + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, w)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		q = `CREATE TABLE "ip_été_default"(a BOOL CONSTRAINT ip_default_name DEFAULT (COALESCE(pg_catalog.pg_indexam_has_property(403,'can_order'),false)))`
		if b.PG {
			run(fixture.Exec(q, 0))
			run(fixture.Exec(`INSERT INTO "ip_été_default" DEFAULT VALUES`, 1))
			run(indexPropertiesBoolStep(`SELECT a FROM "ip_été_default"`, []string{"a"}, true))
		} else {
			w = fixture.ControlsReferenceDiagnostic("0A000", "functions are unsupported")
			w.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "pg_catalog")]) + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, w)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
	})
}

func indexPropertiesSmokeStep(name string) fixture.Step {
	q := "SELECT pg_indexam_has_property(403,'can_order') AS am_order,pg_index_has_property('" + name + "','backward_scan') AS backward,pg_index_column_has_property('" + name + "',1,'desc') AS desc1,pg_index_column_has_property('" + name + "',1,'nulls_last') AS last1,pg_index_column_has_property('" + name + "',2,'asc') AS asc2,pg_index_column_has_property('" + name + "',2,'nulls_first') AS first2,pg_index_column_has_property('" + name + "',3,'returnable') AS payload_return,pg_index_column_has_property('" + name + "',3,'orderable') AS payload_order,pg_index_column_has_property('" + name + "',3,'asc') AS payload_asc,pg_index_column_has_property('" + name + "',4,'asc') AS bad_column,pg_index_has_property('" + name + "','bogus') AS unknown_prop"
	return indexPropertiesBoolStep(q, []string{"am_order", "backward", "desc1", "last1", "asc2", "first2", "payload_return", "payload_order", "payload_asc", "bad_column", "unknown_prop"}, true, true, true, true, true, true, true, false, nil, nil, nil)
}

func TestReferenceIndexPropertiesFeatureSmoke(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE index_properties_smoke(a INT,b INT,p TEXT)", 0), fixture.Exec("CREATE INDEX index_properties_smoke_ix ON index_properties_smoke(a DESC NULLS LAST,b ASC NULLS FIRST) INCLUDE(p)", 0)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		check := func(db fixture.Executor, recorder *fixture.Recorder) {
			fixture.RunStep(t, p.Ctx, db, recorder, indexPropertiesSmokeStep("index_properties_smoke_ix"))
			for _, tc := range []struct {
				n    int
				prop string
				want any
			}{{1, "DESC", true}, {3, "orderable", false}, {3, "asc", nil}} {
				s := indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, tc.want)
				s.Args = []any{"index_properties_smoke_ix", tc.n, tc.prop}
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
			}
		}
		check(c, b.Recorder)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b.Recorder)
		other := fixture.UpdateReturningNativeDestination(t)
		if err := stuntgres.Restore(p.Ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		rows, err := other.QueryContext(p.Ctx, indexPropertiesSmokeStep("index_properties_smoke_ix").Query)
		fixture.DeleteReferenceConsume(t, false, rows, err, indexPropertiesSmokeStep("index_properties_smoke_ix"))
	})
}

func TestReferenceIndexPropertiesNativeControls(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE VIEW ip_stored AS SELECT pg_index_column_has_property('ip_mixed'::regclass,2,'desc') AS result", 0))
		run(fixture.Exec("CREATE VIEW ip_null_stored AS SELECT pg_index_has_property(NULL::regclass,'index_scan') AS result", 0))
		run(fixture.Exec("CREATE VIEW ip_unique_stored AS SELECT pg_index_has_property('ip_unique_a_key'::regclass,'backward_scan') AS result", 0))
		run(fixture.Exec("CREATE TABLE ip_restore_check(n INT CHECK(pg_index_column_has_property('ip_mixed'::regclass,n,'orderable') IS TRUE))", 0))
		check := func(db fixture.Executor, recorder *fixture.Recorder, name string) {
			for _, tc := range []struct {
				n    int
				prop string
				want any
			}{{1, "nulls_first", true}, {2, "nulls_last", true}, {3, "desc", true}, {4, "asc", true}, {5, "returnable", true}, {5, "orderable", false}, {5, "asc", nil}} {
				s := indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, tc.want)
				s.Args = []any{name, tc.n, tc.prop}
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
			}
			fixture.RunStep(t, p.Ctx, db, recorder, indexPropertiesBoolStep("SELECT result FROM ip_stored", []string{"result"}, true))
			fixture.RunStep(t, p.Ctx, db, recorder, indexPropertiesBoolStep("SELECT result FROM ip_null_stored", []string{"result"}, nil))
			fixture.RunStep(t, p.Ctx, db, recorder, indexPropertiesBoolStep("SELECT result FROM ip_unique_stored", []string{"result"}, true))
			fixture.RunStep(t, p.Ctx, db, recorder, indexPropertiesBoolStep("SELECT pg_index_has_property('ip_unique_a_key','backward_scan') AS result", []string{"result"}, true))
		}
		check(c, b.Recorder, "ip_mixed")
		run(fixture.Exec("ALTER TABLE ip_data RENAME TO ip_renamed", 0))
		run(fixture.Exec("ALTER TABLE ip_renamed RENAME COLUMN a TO renamed_a", 0))
		check(c, b.Recorder, "ip_mixed")
		if b.PG {
			return
		}
		var before, after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		check(c, b.Recorder, "ip_mixed")
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("inquiry mutated dump")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b.Recorder, "ip_mixed")
		other := fixture.UpdateReturningNativeDestination(t)
		// Shift destination identities; restored queries must resolve destination objects.
		if _, err := other.ExecContext(p.Ctx, "CREATE TEMP SEQUENCE ip_identity_shift"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, other, bytes.NewReader(before.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			n    int
			prop string
			want any
		}{{1, "nulls_first", true}, {2, "nulls_last", true}, {3, "desc", true}, {4, "asc", true}, {5, "returnable", true}, {5, "orderable", false}, {5, "asc", nil}} {
			s := indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, tc.want)
			rows, err := other.QueryContext(p.Ctx, s.Query, "ip_mixed", tc.n, tc.prop)
			fixture.DeleteReferenceConsume(t, false, rows, err, s)
		}
		for _, s := range []fixture.Step{indexPropertiesBoolStep("SELECT result FROM ip_stored", []string{"result"}, true), indexPropertiesBoolStep("SELECT result FROM ip_null_stored", []string{"result"}, nil), indexPropertiesBoolStep("SELECT result FROM ip_unique_stored", []string{"result"}, true), indexPropertiesBoolStep("SELECT pg_index_has_property('ip_unique_a_key','backward_scan') AS result", []string{"result"}, true)} {
			rows, err := other.QueryContext(p.Ctx, s.Query)
			fixture.DeleteReferenceConsume(t, false, rows, err, s)
		}
		result, err := other.ExecContext(p.Ctx, "INSERT INTO ip_restore_check VALUES(2)")
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		s := fixture.Select("SELECT n FROM ip_restore_check", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}})
		rows, err := other.QueryContext(p.Ctx, s.Query)
		fixture.DeleteReferenceConsume(t, false, rows, err, s)
	})
}
