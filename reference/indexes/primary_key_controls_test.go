package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePrimaryKeyFeatureSmoke(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE s(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,payload TEXT,PRIMARY KEY(a,b))", 0))
		run(fixture.Exec("INSERT INTO s VALUES(7)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1,$2,$3)", 1, int64(1), "x", "keep")))
		run(fixture.Exec("INSERT INTO t VALUES(1,'y','other')", 1))
		want := fixture.Select("SELECT * FROM t ORDER BY a,b", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "keep"}, {int64(1), "y", "other"}})
		for _, s := range []fixture.Step{
			fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES($1,$2,$3)", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists."), int64(1), "x", "bad")),
			fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL,'z','bad')", fixture.PrimaryKeyReferenceNull("t", "a", "null, z, bad")),
			fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,'z','valid'),(1,'x','bad')", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")),
			fixture.ErrorDiagnostic("UPDATE t SET b='x' WHERE b='y'", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")),
		} {
			run(s)
			run(want)
		}
		run(fixture.Prepared(want))
		fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, want.Query, want.Columns, want.Types, []bool{false, false, true}, 2)
		run(fixture.PrimaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}, {"t_b_not_null", "n", "NOT NULL b", nil}, {"t_pkey", "p", "PRIMARY KEY (a, b)", nil}}))
		fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey")
		run(fixture.Exec("CREATE TABLE life(a INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO life VALUES(1)", 1))
		run(fixture.ErrorDiagnostic("ALTER TABLE life ALTER a DROP NOT NULL", fixture.PrimaryKeyReferenceProtected("a")))
		run(fixture.ErrorDiagnostic("ALTER TABLE life DROP CONSTRAINT life_a_not_null", fixture.PrimaryKeyReferenceProtected("a")))
		run(fixture.Exec("ALTER TABLE life DROP CONSTRAINT life_pkey", 0))
		run(fixture.Exec("INSERT INTO life VALUES(1)", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO life VALUES(NULL)", fixture.PrimaryKeyReferenceNull("life", "a", "null")))
		run(fixture.Exec("ALTER TABLE life DROP CONSTRAINT life_a_not_null", 0))
		run(fixture.Exec("INSERT INTO life VALUES(NULL)", 1))
		run(fixture.Select("SELECT a FROM life ORDER BY a NULLS LAST", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {nil}}))
		primaryKeyReferenceRoundtrip(t, p, b, want)
	})
}

func TestReferencePrimaryKeyNativeControls(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE s(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,payload TEXT,PRIMARY KEY(a,b))", 0))
		run(fixture.Exec("INSERT INTO s VALUES(7)", 1))
		run(fixture.Exec("INSERT INTO t VALUES(1,'x','keep'),(1,'y','other')", 2))
		primaryKeyReferenceRoundtrip(t, p, b, fixture.Select("SELECT * FROM t ORDER BY a,b", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "keep"}, {int64(1), "y", "other"}}))
	})
}

func primaryKeyReferenceRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, want fixture.Step) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	query := "INSERT INTO t VALUES($1,$2,$3)"
	held, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if !b.PG {
		old, err := b.DB.QueryContext(p.Ctx, want.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Count(dump.String(), `CONSTRAINT "t_pkey" PRIMARY KEY (a, b)`) != 1 || strings.Count(dump.String(), `CONSTRAINT "s_pkey" PRIMARY KEY (id)`) != 1 || strings.Contains(dump.String(), "CREATE UNIQUE INDEX") {
			t.Fatal("PK must be inline exactly once, never a second standalone backing", dump.String())
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		primaryKeyReferenceRetainedRows(t, old, want)
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
			t.Fatal("noncanonical PK roundtrip", err, again.String())
		}
	}
	// PostgreSQL has no Reset/Dump/Restore API. These SQL outcomes are the same
	// independently specified persistence/enforcement contract in both modes.
	run(want)
	run(fixture.Prepared(want))
	run(fixture.Select("SELECT id FROM s", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, want.Query, want.Columns, want.Types, []bool{false, false, true}, len(want.Rows))
	fixture.PrimaryKeyReferenceIDs(t, p, b, "s_pkey")
	fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey")
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: query}, b.Recorder, fixture.ErrorDiagnostic(query, fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists."), int64(1), "x", "bad"))
	run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL,'z','bad')", fixture.PrimaryKeyReferenceNull("t", "a", "null, z, bad")))
	if !b.PG {
		run(fixture.Exec("CREATE TABLE sentinel(id INT)", 0))
		run(fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			sql        string
			diagnostic fixture.Diagnostic
		}{
			{"INSERT INTO public.t VALUES(1,'x','bad');", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")},
			{"INSERT INTO public.t VALUES(NULL,'z','bad');", fixture.PrimaryKeyReferenceNull("t", "a", "null, z, bad")},
		} {
			old, err := b.DB.QueryContext(p.Ctx, want.Query)
			if err != nil {
				t.Fatal(err)
			}
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+c.sql))
			fixture.AssertDiagnostic(t, err, &c.diagnostic)
			primaryKeyReferenceRetainedRows(t, old, want)
			run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(91)}}))
			run(want)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != before.String() {
				t.Fatal("failed Restore changed catalog/data", err, after.String())
			}
		}
	}
}

func primaryKeyReferenceRetainedRows(t *testing.T, rows *sql.Rows, want fixture.Step) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	fixture.TableDDLColumnTypes(t, false, columns, want.Columns, want.Types, []bool{false, false, true})
	var got [][]any
	for rows.Next() {
		var a, b, payload any
		if err := rows.Scan(&a, &b, &payload); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{a, b, payload})
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, want.Rows) {
		t.Fatal("held eager rows changed", got, want.Rows, rows.Err())
	}
}

func TestReferencePrimaryKeyTemporaryControls(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE persistent(a INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO persistent VALUES(7)", 1))
		run(fixture.Exec("CREATE TEMP TABLE retained_pk(a INT,b TEXT,PRIMARY KEY(a,b))", 0))
		run(fixture.Exec("INSERT INTO retained_pk VALUES(1,'x')", 1))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("CREATE TEMP TABLE retained_pk(a INT PRIMARY KEY)", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO retained_pk VALUES(81)", 1))
		var namespace string
		run(fixture.Exec("SET search_path=pg_temp,public", 0))
		if err := a.QueryRowContext(p.Ctx, "SELECT current_schema").Scan(&namespace); err != nil || namespace == "" {
			t.Fatal(namespace, err)
		}
		run(fixture.Exec("SET search_path=public", 0))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "retained_pk") || strings.Contains(dump.String(), "pg_temp_") {
				t.Fatal("TEMP PK leaked into Dump", dump.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Select("SELECT * FROM retained_pk", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}}))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT a FROM retained_pk", []string{"a"}, []string{"INT4"}, [][]any{{int64(81)}}))
		duplicate := fixture.UniqueReferenceDuplicate("retained_pk", "retained_pk_pkey", "Key (a, b)=(1, x) already exists.")
		duplicate.Schema = namespace
		run(fixture.ErrorDiagnostic("INSERT INTO retained_pk VALUES(1,'x')", duplicate))
		null := fixture.PrimaryKeyReferenceNull("retained_pk", "a", "null, x")
		null.Schema = namespace
		run(fixture.ErrorDiagnostic("INSERT INTO retained_pk VALUES(NULL,'x')", null))
		run(fixture.Select("SELECT a FROM persistent", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("DROP TABLE retained_pk", 0))
	})
}
