package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
)

func regtypeControlSetup(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
	t.Helper()
	regtypeValuesSetup(t, p, conn, recorder)
	for _, q := range []string{
		"CREATE TABLE regtype_a.row_referent(v integer)",
		"CREATE TABLE regtype_a.references_subject(id integer,r regtype,d regtype_a.identifier,a regtype[])",
		"CREATE DOMAIN regtype_a.identifier_array AS regtype[]",
		"CREATE TABLE regtype_a.recursive_control(id integer,d regtype_a.identifier[],a regtype_a.identifier_array)",
		"CREATE VIEW regtype_a.reference_view AS SELECT 'regtype_a.same'::regtype AS r",
		"CREATE TABLE regtype_a.expression_subject(id integer,r regtype DEFAULT 'regtype_a.same'::regtype,CHECK(r='regtype_a.same'::regtype))",
		"CREATE TABLE regtype_a.generated_subject(id integer,r regtype GENERATED ALWAYS AS ('regtype_a.same'::regtype) STORED)",
		"CREATE INDEX regtype_reference_index ON regtype_a.references_subject ((r='regtype_a.same'::regtype))",
	} {
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 0))
	}
	referenceRunStep(t, p.ctx, conn, recorder, referenceExec(`INSERT INTO regtype_a.references_subject VALUES
 (1,'integer','regtype_a.same','{integer,regtype_a.same,NULL}'),
 (2,'regtype_a.mood','regtype_a.same[]','{regtype_a.mood,regtype_a.same[]}'),
 (3,'"Odd.Regtype"."a""b.dot"','regtype_a.row_referent','{regtype_a.row_referent,regtype_a.row_referent[]}'),
 (4,'-','4294967295','{}'),(5,NULL,NULL,NULL)`, 5))
	referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_a.expression_subject(id) VALUES(1)", 1))
	referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_a.generated_subject(id) VALUES(1)", 1))
	referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_a.recursive_control VALUES(1,'{integer,regtype_a.same,NULL}','{regtype_a.row_referent,regtype_a.same[]}'),(2,NULL,NULL)", 2))
}

func regtypeControlRows() referenceStep {
	s := referenceSelect("SELECT id,r,d,a FROM regtype_a.references_subject ORDER BY id", []string{"id", "r", "d", "a"}, []string{"INT4", "REGTYPE", "REGTYPE", "_REGTYPE"}, [][]any{
		{int64(1), "integer", "same", "{integer,same,NULL}"},
		{int64(2), "mood", "same[]", "{mood,same[]}"},
		{int64(3), `"Odd.Regtype"."a""b.dot"`, "row_referent", "{row_referent,row_referent[]}"},
		{int64(4), "-", "4294967295", "{}"}, {int64(5), nil, nil, nil},
	})
	s.checkMetadata = regtypeMetadata
	return s
}

func regtypeControlCheck(t *testing.T, p *referencePair, db referenceExecutor, recorder *referenceRecorder) {
	t.Helper()
	referenceRunStep(t, p.ctx, db, recorder, referenceExec("SET search_path TO regtype_a,regtype_b,public", 0))
	regtypeBoth(t, p, db, recorder, regtypeValuesRows(recorder.rawBackend == referenceRawPostgres))
	regtypeBoth(t, p, db, recorder, regtypeControlRows())
	for _, query := range []string{"SELECT r FROM regtype_a.reference_view", "SELECT r FROM regtype_a.expression_subject", "SELECT r FROM regtype_a.generated_subject"} {
		regtypeBoth(t, p, db, recorder, referenceSelect(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"same"}}))
	}
	regtypeBoth(t, p, db, recorder, referenceSelect("SELECT (r::oid='regtype_a.mood'::regtype::oid) AS same_type,(d::oid='regtype_a.same[]'::regtype::oid) AS same_array FROM regtype_a.references_subject WHERE id=2", []string{"same_type", "same_array"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	regtypeBoth(t, p, db, recorder, referenceSelect("SELECT (d::oid='regtype_a.row_referent'::regtype::oid) AS same_row FROM regtype_a.references_subject WHERE id=3", []string{"same_row"}, []string{"BOOL"}, [][]any{{true}}))
	const recursiveQuery = "SELECT d,a FROM regtype_a.recursive_control ORDER BY id"
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		var statement *sql.Stmt
		if prepared {
			statement, err = db.PrepareContext(p.ctx, recursiveQuery)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = statement.QueryContext(p.ctx)
		} else {
			rows, err = db.QueryContext(p.ctx, recursiveQuery)
		}
		if err != nil {
			if statement != nil {
				statement.Close()
			}
			t.Fatal(err)
		}
		pg := recorder.rawBackend == referenceRawPostgres
		arrayType := "_IDENTIFIER"
		if pg {
			arrayType = ""
		}
		regtypeCursorMetadata(t, rows, pg, []string{"d", "a"}, []string{arrayType, "_REGTYPE"}, []bool{true, true})
		regtypeAssertRows(t, rows, [][]any{{"{integer,same,NULL}", "{row_referent,same[]}"}, {nil, nil}}, pg)
		if statement != nil {
			if err := statement.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestReferenceRegtypeNativeControls(t *testing.T) {
	var emitted []byte
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeControlSetup(t, p, conn, recorder)
		regtypeControlCheck(t, p, conn, recorder)
		if backend.pg {
			if emitted == nil {
				t.Fatal("actual native Dump stream required for PostgreSQL replay")
			}
			for _, schema := range []string{"regtype_a", "regtype_b", "Odd.Regtype"} {
				referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP SCHEMA "+regtypeIdentifier(schema)+" CASCADE", 0))
			}
			if _, err := conn.ExecContext(p.ctx, string(emitted)); err != nil {
				t.Fatal("actual native emitted stream PostgreSQL replay", err)
			}
			regtypeControlCheck(t, p, conn, recorder)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		regtypeControlCheck(t, p, conn, recorder)
		retained, err := conn.QueryContext(p.ctx, regtypeControlRows().query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		regtypeMetadata(t, retained)
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TEMP SEQUENCE regtype_retained_temp", 0))
		var tempBefore int64
		if err := conn.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE relname='regtype_retained_temp'").Scan(&tempBefore); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		regtypeControlCheck(t, p, conn, recorder)
		regtypeAssertRows(t, retained, regtypeControlRows().rows, false)
		var tempAfter int64
		if err := conn.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE relname='regtype_retained_temp'").Scan(&tempAfter); err != nil {
			t.Fatal(err)
		}
		if tempBefore != tempAfter {
			t.Fatal("restore changed retained TEMP sequence identity", tempBefore, tempAfter)
		}
		target := newReferenceFixture(t, false, true)
		for _, q := range []string{"CREATE SCHEMA regtype_spacer", "CREATE DOMAIN regtype_spacer.d AS integer", "CREATE TABLE regtype_spacer.t(v integer)"} {
			referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec(q, 0))
		}
		if err := stuntgres.Restore(target.ctx, target.native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		regtypeControlCheck(t, target, target.memory, target.memoryRecord)
		// A second emitted stream and source Reset must not retain source descriptors.
		var second bytes.Buffer
		if err := stuntgres.Dump(target.ctx, target.native, &second); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(target.ctx, target.native, bytes.NewReader(second.Bytes())); err != nil {
			t.Fatal(err)
		}
		regtypeControlCheck(t, target, target.memory, target.memoryRecord)
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec("CREATE TABLE public.regtype_sentinel(v integer)", 0))
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec("INSERT INTO public.regtype_sentinel VALUES(42)", 1))
		sentinel := referenceSelect("SELECT v FROM public.regtype_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		for _, bad := range []string{
			"CREATE SCHEMA regtype_staged; CREATE DOMAIN regtype_staged.d AS integer; CREATE TABLE regtype_staged.t(r regtype); INSERT INTO regtype_staged.t VALUES('missing_regtype_name');",
			second.String() + " SELECT (",
		} {
			if err := stuntgres.Restore(target.ctx, target.native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid Restore succeeded")
			}
			referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, sentinel)
			regtypeControlCheck(t, target, target.memory, target.memoryRecord)
		}
		ctx, cancel := context.WithCancel(target.ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, sentinel)
	})
}

func TestReferenceRegtypeStoredDependencies(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeControlSetup(t, p, conn, recorder)
		result, err := conn.ExecContext(p.ctx, "DROP DOMAIN regtype_a.same RESTRICT")
		if result != nil {
			t.Error("restricted drop returned result")
		}
		regtypeAssertError(t, err, "2BP01", "", "DROP DOMAIN regtype_a.same RESTRICT", "stored-expression-dependencies", "execute")
		regtypeControlCheck(t, p, conn, recorder)
		// Data-only references do not own the referenced identity.
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE DOMAIN regtype_a.data_only AS integer", 0))
		var oid int64
		if err := conn.QueryRowContext(p.ctx, "SELECT 'regtype_a.data_only'::regtype::bigint").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_a.references_subject(id,r) VALUES(6,'regtype_a.data_only')", 1))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP DOMAIN regtype_a.data_only", 0))
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT (r::bigint=$1) AS same_id FROM regtype_a.references_subject WHERE id=6", []string{"same_id"}, []string{"BOOL"}, [][]any{{true}}, oid))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE VIEW regtype_a.row_dependency AS SELECT 'regtype_a.row_referent'::regtype AS r", 0))
		result, err = conn.ExecContext(p.ctx, "DROP TABLE regtype_a.row_referent RESTRICT")
		if result != nil {
			t.Error("row restricted drop returned result")
		}
		regtypeAssertError(t, err, "2BP01", "", "DROP TABLE regtype_a.row_referent RESTRICT", "row-owning-relation", "execute")
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP TABLE regtype_a.row_referent CASCADE", 0))
		*notices = nil
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP DOMAIN regtype_a.same CASCADE", 0))
		regtypeAssertNotices(t, *notices, []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 6 other objects", Detail: "drop cascades to default value for column fallback of table values_subject\ndrop cascades to view reference_view\ndrop cascades to default value for column r of table expression_subject\ndrop cascades to constraint expression_subject_r_check on table expression_subject\ndrop cascades to column r of table generated_subject\ndrop cascades to index regtype_reference_index"}})
		for _, query := range []string{"SELECT * FROM regtype_a.reference_view", "SELECT * FROM regtype_a.row_dependency"} {
			rows, err := conn.QueryContext(p.ctx, query)
			if rows != nil {
				rows.Close()
				t.Error("cascade retained dependent view")
			}
			regtypeAssertError(t, err, "42P01", "", query, "cascade-closure", "execute")
		}
		// The observed six-object closure drops definitions/the generated column,
		// not their owning tables, plain regtype values or unrelated scalar columns.
		rows, err := conn.QueryContext(p.ctx, "SELECT id FROM regtype_a.references_subject ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		regtypeAssertRows(t, rows, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}}, backend.pg)
		for _, fact := range []struct {
			table string
			ids   [][]any
		}{
			{"expression_subject", [][]any{{int64(1)}}},
			{"generated_subject", [][]any{{int64(1)}}},
			{"recursive_control", [][]any{{int64(1)}, {int64(2)}}},
			{"values_subject", [][]any{{int64(1)}, {int64(2)}, {int64(3)}}},
		} {
			query := "SELECT id FROM regtype_a." + fact.table + " ORDER BY id"
			if fact.table == "generated_subject" {
				query = "SELECT * FROM regtype_a.generated_subject ORDER BY id"
			}
			regtypeBoth(t, p, conn, recorder, referenceSelect(query, []string{"id"}, []string{"INT4"}, fact.ids))
		}
	})
}

func TestReferenceRegtypeSchemaRenameSnapshots(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeSetup(t, p, conn, recorder)
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE regtype_a.rename_subject(r regtype)", 0))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_a.rename_subject VALUES('regtype_a.same')", 1))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO public", 0))
		retained, err := backend.db.QueryContext(p.ctx, "SELECT r FROM regtype_a.rename_subject")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var before int64
		if err := conn.QueryRowContext(p.ctx, "SELECT 'regtype_a.same'::regtype::bigint").Scan(&before); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("ALTER SCHEMA regtype_a RENAME TO regtype_renamed", 0))
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT r,(r::bigint=$1) AS stable FROM regtype_renamed.rename_subject", []string{"r", "stable"}, []string{"REGTYPE", "BOOL"}, [][]any{{"regtype_renamed.same", true}}, before))
		regtypeCursorMetadata(t, retained, backend.pg, []string{"r"}, []string{"REGTYPE"}, []bool{true})
		regtypeAssertRows(t, retained, [][]any{{"regtype_a.same"}}, backend.pg)
	})
}
