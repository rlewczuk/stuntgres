package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineLanguagesDependencies(t *testing.T) {
	t.Run("edges_and_retained_catalog", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_edges CASCADE")()
			userOperatorExec(t, p, b, c, "CREATE TRUSTED LANGUAGE rl_edges"+routineLanguageFull,
				"CREATE FUNCTION rl_edges_fn(x integer) RETURNS integer LANGUAGE rl_edges AS $$BEGIN RETURN x+1; END$$")
			q := "SELECT oid,lanname,lanowner,lanispl,lanpltrusted,lanplcallfoid,laninline,lanvalidator,lanacl FROM pg_language WHERE lanname='rl_edges'"
			rows, err := c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			wantNames := []string{"oid", "lanname", "lanowner", "lanispl", "lanpltrusted", "lanplcallfoid", "laninline", "lanvalidator", "lanacl"}
			wantTypes := []string{"OID", "NAME", "OID", "BOOL", "BOOL", "OID", "OID", "OID", "_ACLITEM"}
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(cols) != 9 || len(types) != 9 {
				t.Fatalf("language shape: %v %v", cols, types)
			}
			for i := range cols {
				if cols[i] != wantNames[i] || types[i].DatabaseTypeName() != wantTypes[i] {
					t.Fatalf("column %d: %s/%s", i, cols[i], types[i].DatabaseTypeName())
				}
			}
			// Consume the oracle stream before mutating on its physical session;
			// native eager rows remain deliberately unread until after replacement.
			var before []any
			if b.PG {
				before = routineLanguagesScanRow(t, rows)
				rows.Close()
			}
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid JOIN pg_proc h ON d.refclassid='pg_proc'::regclass AND d.refobjid=h.oid WHERE l.lanname='rl_edges' AND d.deptype='n' AND d.objsubid=0 AND d.refobjsubid=0 AND h.proname IN ('plpgsql_call_handler','plpgsql_inline_handler','plpgsql_validator')", "INT8", int64(3))
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_proc p ON d.classid='pg_proc'::regclass AND d.objid=p.oid JOIN pg_language l ON d.refclassid='pg_language'::regclass AND d.refobjid=l.oid WHERE p.proname='rl_edges_fn' AND l.lanname='rl_edges' AND p.prolang=l.oid AND d.deptype='n'", "INT8", int64(1))
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRUSTED LANGUAGE rl_edges"+routineLanguageCall)
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid WHERE l.lanname='rl_edges' AND d.refclassid='pg_proc'::regclass AND d.deptype='n'", "INT8", int64(1))
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid WHERE l.lanname='rl_edges' AND d.refobjid=0", "INT8", int64(0))
			// SQL's pinned identity does not acquire a manufactured procedural handler.
			plpgsqlValue(t, p, b, c, "SELECT lanplcallfoid=0 AND laninline=0 AS v FROM pg_language WHERE lanname='sql'", "BOOL", true)
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend WHERE refclassid='pg_language'::regclass AND refobjid=14", "INT8", int64(0))
			if !b.PG {
				before = routineLanguagesScanRow(t, rows)
				if before[1] != "rl_edges" || before[3] != true || before[4] != true || before[8] != nil {
					t.Fatalf("retained row changed: %#v", before)
				}
				for _, i := range []int{5, 6, 7} {
					if before[i] == nil || before[i] == "0" || before[i] == int64(0) {
						t.Fatalf("retained handler disappeared: %#v", before)
					}
				}
			}
			plpgsqlValue(t, p, b, c, "SELECT rl_edges_fn(4) AS v", "INT4", int64(5))
		})
	})
	t.Run("handler_retirement", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_retire CASCADE")()
			userOperatorExec(t, p, b, c, "CREATE TRUSTED LANGUAGE rl_retire"+routineLanguageCall, "BEGIN", "ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_call_handler()")
			d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function plpgsql_call_handler() because other objects depend on it")
			d.Detail = "extension plpgsql depends on function plpgsql_call_handler()\nlanguage rl_retire depends on function plpgsql_call_handler()"
			d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			// An expected error aborts this transaction. Run each route in its
			// own transaction so extension membership is always rolled back.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("DROP FUNCTION pg_catalog.plpgsql_call_handler()", d))
			userOperatorExec(t, p, b, c, "ROLLBACK", "BEGIN", "ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_call_handler()")
			s := fixture.Prepared(fixture.ErrorDiagnostic("DROP FUNCTION pg_catalog.plpgsql_call_handler()", d))
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			userOperatorExec(t, p, b, c, "ROLLBACK", "CREATE FUNCTION rl_retire_fn(x integer) RETURNS integer LANGUAGE rl_retire AS $$BEGIN RETURN x+1; END$$")
			plpgsqlValue(t, p, b, c, "SELECT rl_retire_fn(4) AS v", "INT4", int64(5))
		})
	})
	t.Run("if_exists_list", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			var notices []fixture.Diagnostic
			_, owner, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer routineLanguagesCleanup(t, owner, "DROP LANGUAGE IF EXISTS rl_list CASCADE")()
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, owner, recorder, fixture.Exec("CREATE TRUSTED LANGUAGE rl_list"+routineLanguageCall, 0))
				s := fixture.Exec("DROP LANGUAGE IF EXISTS rl_list_missing,rl_list", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, owner, recorder, s)
			}
			want := []fixture.Diagnostic{
				{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `language "rl_list_missing" does not exist, skipping`},
				{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `language "rl_list_missing" does not exist, skipping`},
			}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("language IF EXISTS notices: %#v want %#v", notices, want)
			}
		})
	})
	t.Run("restrict_and_transitive_cascade", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			var notices []fixture.Diagnostic
			_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			c = noticeConn
			b.Recorder = recorder
			defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_dep CASCADE")()
			userOperatorExec(t, p, b, c, "CREATE TRUSTED LANGUAGE rl_dep"+routineLanguageFull,
				"CREATE FUNCTION rl_dep_fn(x integer) RETURNS integer LANGUAGE rl_dep AS $$BEGIN RETURN x+1; END$$",
				"CREATE VIEW rl_dep_view AS SELECT rl_dep_fn(4) AS v",
				"CREATE VIEW rl_dep_chain AS SELECT v FROM rl_dep_view",
				"CREATE TABLE rl_dep_data(id integer,v integer DEFAULT rl_dep_fn(4),CONSTRAINT rl_dep_check CHECK(rl_dep_fn(v)>0))",
				"CREATE TABLE rl_dep_keep(v integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_dep_data(id) VALUES(1)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_dep_keep VALUES(7)", 1))
			d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop language rl_dep because other objects depend on it")
			d.Detail = "function rl_dep_fn(integer) depends on language rl_dep\nview rl_dep_view depends on function rl_dep_fn(integer)\nview rl_dep_chain depends on view rl_dep_view\ndefault value for column v of table rl_dep_data depends on function rl_dep_fn(integer)\nconstraint rl_dep_check on table rl_dep_data depends on function rl_dep_fn(integer)"
			d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			for _, suffix := range []string{"", " RESTRICT"} {
				routineLanguagesError(t, p, b, c, "DROP LANGUAGE rl_dep"+suffix, d, false, "execute")
			}
			routineLanguagesMissing(t, p, b, c, "DROP LANGUAGE rl_dep,rl_missing CASCADE", "rl_missing")
			plpgsqlValue(t, p, b, c, "SELECT v FROM rl_dep_chain", "INT4", int64(5))
			plpgsqlValue(t, p, b, c, "SELECT v FROM rl_dep_data WHERE id=1", "INT4", int64(5))
			userOperatorExec(t, p, b, c, "DROP LANGUAGE rl_dep CASCADE")
			wantNotices := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 5 other objects", Detail: "drop cascades to function rl_dep_fn(integer)\ndrop cascades to view rl_dep_view\ndrop cascades to view rl_dep_chain\ndrop cascades to default value for column v of table rl_dep_data\ndrop cascades to constraint rl_dep_check on table rl_dep_data"}}
			if !reflect.DeepEqual(notices, wantNotices) {
				t.Fatalf("language cascade notices: %#v want %#v", notices, wantNotices)
			}
			routineLanguagesCount(t, p, b, c, "rl_dep", 0)
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rl_dep_fn'", "INT8", int64(0))
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname IN ('rl_dep_view','rl_dep_chain')", "INT8", int64(0))
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_attrdef WHERE adrelid='rl_dep_data'::regclass", "INT8", int64(0))
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_constraint WHERE conrelid='rl_dep_data'::regclass", "INT8", int64(0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_dep_data(id) VALUES(2)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM rl_dep_data ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(5)}, {int64(2), nil}}))
			plpgsqlValue(t, p, b, c, "SELECT v FROM rl_dep_keep", "INT4", int64(7))
		})
	})
}

func routineLanguagesScanRow(t *testing.T, rows *sql.Rows) []any {
	t.Helper()
	values := make([]any, 9)
	ptrs := make([]any, 9)
	for i := range values {
		ptrs[i] = &values[i]
	}
	if !rows.Next() {
		t.Fatal("language row missing", rows.Err())
	}
	if err := rows.Scan(ptrs...); err != nil {
		t.Fatal(err)
	}
	for i := range values {
		values[i] = fixture.Normalize(values[i])
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("language row cardinality", rows.Err())
	}
	return values
}
