package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestReferenceIndexPropertiesConsumers(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, s := range []referenceStep{
			referenceSelect("SELECT a FROM ip_data WHERE pg_index_column_has_property('ip_mixed',a,'desc') IS TRUE ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}),
			referenceSelect("SELECT x.a FROM ip_data x JOIN ip_data y ON x.a=y.a AND pg_index_has_property('ip_default','index_scan') IS TRUE ORDER BY x.a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			indexPropertiesBoolStep("SELECT CASE WHEN pg_index_column_has_property('ip_mixed',5,'asc') IS NULL THEN pg_index_has_property('ip_default','index_scan') ELSE false END AS result FROM ip_data WHERE a=1", []string{"result"}, true),
			indexPropertiesBoolStep("SELECT COALESCE(pg_index_column_has_property('ip_mixed',5,'asc'),false) AS result FROM ip_data WHERE a=1", []string{"result"}, false),
			referenceSelect("SELECT pg_index_column_has_property('ip_mixed',a,'desc') AS result,count(*) AS n FROM ip_data GROUP BY pg_index_column_has_property('ip_mixed',a,'desc') ORDER BY result", []string{"result", "n"}, []string{"BOOL", "INT8"}, [][]any{{false, int64(1)}, {true, int64(1)}}),
			indexPropertiesBoolStep("SELECT pg_index_column_has_property('ip_mixed',max(a),'desc') AS result FROM ip_data", []string{"result"}, true),
			referenceSelect("SELECT count(pg_index_column_has_property('ip_mixed',a,'asc')) AS n FROM ip_data", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
			referenceExec("CREATE VIEW ip_property_view AS SELECT a,pg_index_column_has_property('ip_mixed',a,'desc') AS result FROM ip_data", 0),
			referenceSelect("SELECT result FROM ip_property_view ORDER BY a", []string{"result"}, []string{"BOOL"}, [][]any{{false}, {true}}),
			referenceExec("CREATE TABLE ip_checked(n INT CHECK(pg_index_column_has_property('ip_mixed',n,'orderable') IS TRUE))", 0),
			referenceExec("INSERT INTO ip_checked VALUES(1)", 1),
			referenceExec("UPDATE ip_checked SET n=2", 1),
			referenceSelect("SELECT n FROM ip_checked", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}),
		} {
			run(s)
		}
		w := controlsReferenceDiagnostic("23514", `new row for relation "ip_checked" violates check constraint "ip_checked_n_check"`)
		w.Detail = "Failing row contains (5)."
		w.Schema = "public"
		w.Table = "ip_checked"
		w.Constraint = "ip_checked_n_check"
		run(referenceErrorDiagnostic("INSERT INTO ip_checked VALUES(5)", w))
		limitQuery := "SELECT a FROM ip_data LIMIT CASE WHEN pg_index_has_property(a,'index_scan') THEN 1 ELSE 0 END"
		w = controlsReferenceDiagnostic("42P10", "argument of LIMIT must not contain variables")
		w.Position = referencePosition(strings.Index(limitQuery, "(a,") + 2)
		run(referenceErrorDiagnostic(limitQuery, w))
		w = controlsReferenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE")
		run(referenceErrorDiagnostic("CREATE INDEX ip_stable ON ip_data((pg_index_has_property('ip_default','index_scan')))", w))
		w = controlsReferenceDiagnostic("42P17", "generation expression is not immutable")
		run(referenceErrorDiagnostic("CREATE TABLE ip_generated(a BOOL GENERATED ALWAYS AS (pg_indexam_has_property(403,'can_order')) STORED)", w))
		// The native DEFAULT boundary intentionally differs from PostgreSQL admission.
		q := "CREATE TABLE ip_default_boundary(a BOOL DEFAULT pg_indexam_has_property(403,'can_order'))"
		if b.pg {
			run(referenceExec(q, 0))
			run(referenceExec("INSERT INTO ip_default_boundary DEFAULT VALUES", 1))
			run(indexPropertiesBoolStep("SELECT a FROM ip_default_boundary", []string{"a"}, true))
		} else {
			w = controlsReferenceDiagnostic("0A000", "functions are unsupported")
			w.Position = referencePosition(strings.Index(q, "pg_indexam") + 1)
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic(q, w)
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				run(s)
			}
		}
		q = `CREATE TABLE "ip_été_default"(a BOOL CONSTRAINT ip_default_name DEFAULT (COALESCE(pg_catalog.pg_indexam_has_property(403,'can_order'),false)))`
		if b.pg {
			run(referenceExec(q, 0))
			run(referenceExec(`INSERT INTO "ip_été_default" DEFAULT VALUES`, 1))
			run(indexPropertiesBoolStep(`SELECT a FROM "ip_été_default"`, []string{"a"}, true))
		} else {
			w = controlsReferenceDiagnostic("0A000", "functions are unsupported")
			w.Position = referencePosition(utf8.RuneCountInString(q[:strings.Index(q, "pg_catalog")]) + 1)
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic(q, w)
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				run(s)
			}
		}
	})
}

func indexPropertiesSmokeStep(name string) referenceStep {
	q := "SELECT pg_indexam_has_property(403,'can_order') AS am_order,pg_index_has_property('" + name + "','backward_scan') AS backward,pg_index_column_has_property('" + name + "',1,'desc') AS desc1,pg_index_column_has_property('" + name + "',1,'nulls_last') AS last1,pg_index_column_has_property('" + name + "',2,'asc') AS asc2,pg_index_column_has_property('" + name + "',2,'nulls_first') AS first2,pg_index_column_has_property('" + name + "',3,'returnable') AS payload_return,pg_index_column_has_property('" + name + "',3,'orderable') AS payload_order,pg_index_column_has_property('" + name + "',3,'asc') AS payload_asc,pg_index_column_has_property('" + name + "',4,'asc') AS bad_column,pg_index_has_property('" + name + "','bogus') AS unknown_prop"
	return indexPropertiesBoolStep(q, []string{"am_order", "backward", "desc1", "last1", "asc2", "first2", "payload_return", "payload_order", "payload_asc", "bad_column", "unknown_prop"}, true, true, true, true, true, true, true, false, nil, nil, nil)
}

func TestReferenceIndexPropertiesFeatureSmoke(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE index_properties_smoke(a INT,b INT,p TEXT)", 0), referenceExec("CREATE INDEX index_properties_smoke_ix ON index_properties_smoke(a DESC NULLS LAST,b ASC NULLS FIRST) INCLUDE(p)", 0)} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		check := func(db referenceExecutor, recorder *referenceRecorder) {
			referenceRunStep(t, p.ctx, db, recorder, indexPropertiesSmokeStep("index_properties_smoke_ix"))
			for _, tc := range []struct {
				n    int
				prop string
				want any
			}{{1, "DESC", true}, {3, "orderable", false}, {3, "asc", nil}} {
				s := indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, tc.want)
				s.args = []any{"index_properties_smoke_ix", tc.n, tc.prop}
				referenceRunStep(t, p.ctx, db, recorder, referencePrepared(s))
			}
		}
		check(c, b.recorder)
		if b.pg {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b.recorder)
		other := updateReturningNativeDestination(t)
		if err := stuntgres.Restore(p.ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		rows, err := other.QueryContext(p.ctx, indexPropertiesSmokeStep("index_properties_smoke_ix").query)
		deleteReferenceConsume(t, false, rows, err, indexPropertiesSmokeStep("index_properties_smoke_ix"))
	})
}

func TestReferenceIndexPropertiesNativeControls(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE VIEW ip_stored AS SELECT pg_index_column_has_property('ip_mixed'::regclass,2,'desc') AS result", 0))
		run(referenceExec("CREATE VIEW ip_null_stored AS SELECT pg_index_has_property(NULL::regclass,'index_scan') AS result", 0))
		run(referenceExec("CREATE VIEW ip_unique_stored AS SELECT pg_index_has_property('ip_unique_a_key'::regclass,'backward_scan') AS result", 0))
		run(referenceExec("CREATE TABLE ip_restore_check(n INT CHECK(pg_index_column_has_property('ip_mixed'::regclass,n,'orderable') IS TRUE))", 0))
		check := func(db referenceExecutor, recorder *referenceRecorder, name string) {
			for _, tc := range []struct {
				n    int
				prop string
				want any
			}{{1, "nulls_first", true}, {2, "nulls_last", true}, {3, "desc", true}, {4, "asc", true}, {5, "returnable", true}, {5, "orderable", false}, {5, "asc", nil}} {
				s := indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, tc.want)
				s.args = []any{name, tc.n, tc.prop}
				referenceRunStep(t, p.ctx, db, recorder, referencePrepared(s))
			}
			referenceRunStep(t, p.ctx, db, recorder, indexPropertiesBoolStep("SELECT result FROM ip_stored", []string{"result"}, true))
			referenceRunStep(t, p.ctx, db, recorder, indexPropertiesBoolStep("SELECT result FROM ip_null_stored", []string{"result"}, nil))
			referenceRunStep(t, p.ctx, db, recorder, indexPropertiesBoolStep("SELECT result FROM ip_unique_stored", []string{"result"}, true))
			referenceRunStep(t, p.ctx, db, recorder, indexPropertiesBoolStep("SELECT pg_index_has_property('ip_unique_a_key','backward_scan') AS result", []string{"result"}, true))
		}
		check(c, b.recorder, "ip_mixed")
		run(referenceExec("ALTER TABLE ip_data RENAME TO ip_renamed", 0))
		run(referenceExec("ALTER TABLE ip_renamed RENAME COLUMN a TO renamed_a", 0))
		check(c, b.recorder, "ip_mixed")
		if b.pg {
			return
		}
		var before, after bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
			t.Fatal(err)
		}
		check(c, b.recorder, "ip_mixed")
		if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("inquiry mutated dump")
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(before.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b.recorder, "ip_mixed")
		other := updateReturningNativeDestination(t)
		// Shift destination identities; restored queries must resolve destination objects.
		if _, err := other.ExecContext(p.ctx, "CREATE TEMP SEQUENCE ip_identity_shift"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, other, bytes.NewReader(before.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			n    int
			prop string
			want any
		}{{1, "nulls_first", true}, {2, "nulls_last", true}, {3, "desc", true}, {4, "asc", true}, {5, "returnable", true}, {5, "orderable", false}, {5, "asc", nil}} {
			s := indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, tc.want)
			rows, err := other.QueryContext(p.ctx, s.query, "ip_mixed", tc.n, tc.prop)
			deleteReferenceConsume(t, false, rows, err, s)
		}
		for _, s := range []referenceStep{indexPropertiesBoolStep("SELECT result FROM ip_stored", []string{"result"}, true), indexPropertiesBoolStep("SELECT result FROM ip_null_stored", []string{"result"}, nil), indexPropertiesBoolStep("SELECT result FROM ip_unique_stored", []string{"result"}, true), indexPropertiesBoolStep("SELECT pg_index_has_property('ip_unique_a_key','backward_scan') AS result", []string{"result"}, true)} {
			rows, err := other.QueryContext(p.ctx, s.query)
			deleteReferenceConsume(t, false, rows, err, s)
		}
		result, err := other.ExecContext(p.ctx, "INSERT INTO ip_restore_check VALUES(2)")
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		s := referenceSelect("SELECT n FROM ip_restore_check", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}})
		rows, err := other.QueryContext(p.ctx, s.query)
		deleteReferenceConsume(t, false, rows, err, s)
	})
}
