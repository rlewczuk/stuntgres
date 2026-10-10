package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferencePrimaryKeyFeatureSmoke(t *testing.T) {
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE s(id INT PRIMARY KEY)", 0))
		run(referenceExec("CREATE TABLE t(a INT,b TEXT,payload TEXT,PRIMARY KEY(a,b))", 0))
		run(referenceExec("INSERT INTO s VALUES(7)", 1))
		run(referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2,$3)", 1, int64(1), "x", "keep")))
		run(referenceExec("INSERT INTO t VALUES(1,'y','other')", 1))
		want := referenceSelect("SELECT * FROM t ORDER BY a,b", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "keep"}, {int64(1), "y", "other"}})
		for _, s := range []referenceStep{
			referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES($1,$2,$3)", uniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists."), int64(1), "x", "bad")),
			referenceErrorDiagnostic("INSERT INTO t VALUES(NULL,'z','bad')", primaryKeyReferenceNull("t", "a", "null, z, bad")),
			referenceErrorDiagnostic("INSERT INTO t VALUES(2,'z','valid'),(1,'x','bad')", uniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")),
			referenceErrorDiagnostic("UPDATE t SET b='x' WHERE b='y'", uniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")),
		} {
			run(s)
			run(want)
		}
		run(referencePrepared(want))
		referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, true, want.query, want.columns, want.types, []bool{false, false, true}, 2)
		run(primaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}, {"t_b_not_null", "n", "NOT NULL b", nil}, {"t_pkey", "p", "PRIMARY KEY (a, b)", nil}}))
		primaryKeyReferenceIDs(t, p, b, "t_pkey")
		run(referenceExec("CREATE TABLE life(a INT PRIMARY KEY)", 0))
		run(referenceExec("INSERT INTO life VALUES(1)", 1))
		run(referenceErrorDiagnostic("ALTER TABLE life ALTER a DROP NOT NULL", primaryKeyReferenceProtected("a")))
		run(referenceErrorDiagnostic("ALTER TABLE life DROP CONSTRAINT life_a_not_null", primaryKeyReferenceProtected("a")))
		run(referenceExec("ALTER TABLE life DROP CONSTRAINT life_pkey", 0))
		run(referenceExec("INSERT INTO life VALUES(1)", 1))
		run(referenceErrorDiagnostic("INSERT INTO life VALUES(NULL)", primaryKeyReferenceNull("life", "a", "null")))
		run(referenceExec("ALTER TABLE life DROP CONSTRAINT life_a_not_null", 0))
		run(referenceExec("INSERT INTO life VALUES(NULL)", 1))
		run(referenceSelect("SELECT a FROM life ORDER BY a NULLS LAST", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {nil}}))
		primaryKeyReferenceRoundtrip(t, p, b, want)
	})
}

func TestReferencePrimaryKeyNativeControls(t *testing.T) {
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE s(id INT PRIMARY KEY)", 0))
		run(referenceExec("CREATE TABLE t(a INT,b TEXT,payload TEXT,PRIMARY KEY(a,b))", 0))
		run(referenceExec("INSERT INTO s VALUES(7)", 1))
		run(referenceExec("INSERT INTO t VALUES(1,'x','keep'),(1,'y','other')", 2))
		primaryKeyReferenceRoundtrip(t, p, b, referenceSelect("SELECT * FROM t ORDER BY a,b", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "keep"}, {int64(1), "y", "other"}}))
	})
}

func primaryKeyReferenceRoundtrip(t *testing.T, p *referencePair, b referenceBackend, want referenceStep) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
	query := "INSERT INTO t VALUES($1,$2,$3)"
	held, err := b.db.PrepareContext(p.ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if !b.pg {
		old, err := b.db.QueryContext(p.ctx, want.query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Count(dump.String(), `CONSTRAINT "t_pkey" PRIMARY KEY (a, b)`) != 1 || strings.Count(dump.String(), `CONSTRAINT "s_pkey" PRIMARY KEY (id)`) != 1 || strings.Contains(dump.String(), "CREATE UNIQUE INDEX") {
			t.Fatal("PK must be inline exactly once, never a second standalone backing", dump.String())
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		primaryKeyReferenceRetainedRows(t, old, want)
		var again bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
			t.Fatal("noncanonical PK roundtrip", err, again.String())
		}
	}
	// PostgreSQL has no Reset/Dump/Restore API. These SQL outcomes are the same
	// independently specified persistence/enforcement contract in both modes.
	run(want)
	run(referencePrepared(want))
	run(referenceSelect("SELECT id FROM s", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, true, want.query, want.columns, want.types, []bool{false, false, true}, len(want.rows))
	primaryKeyReferenceIDs(t, p, b, "s_pkey")
	primaryKeyReferenceIDs(t, p, b, "t_pkey")
	referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: held, query: query}, b.recorder, referenceErrorDiagnostic(query, uniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists."), int64(1), "x", "bad"))
	run(referenceErrorDiagnostic("INSERT INTO t VALUES(NULL,'z','bad')", primaryKeyReferenceNull("t", "a", "null, z, bad")))
	if !b.pg {
		run(referenceExec("CREATE TABLE sentinel(id INT)", 0))
		run(referenceExec("INSERT INTO sentinel VALUES(91)", 1))
		var before bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			sql        string
			diagnostic referenceDiagnostic
		}{
			{"INSERT INTO public.t VALUES(1,'x','bad');", uniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")},
			{"INSERT INTO public.t VALUES(NULL,'z','bad');", primaryKeyReferenceNull("t", "a", "null, z, bad")},
		} {
			old, err := b.db.QueryContext(p.ctx, want.query)
			if err != nil {
				t.Fatal(err)
			}
			err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+c.sql))
			referenceAssertDiagnostic(t, err, &c.diagnostic)
			primaryKeyReferenceRetainedRows(t, old, want)
			run(referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(91)}}))
			run(want)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != before.String() {
				t.Fatal("failed Restore changed catalog/data", err, after.String())
			}
		}
	}
}

func primaryKeyReferenceRetainedRows(t *testing.T, rows *sql.Rows, want referenceStep) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	referenceTableDDLColumnTypes(t, false, columns, want.columns, want.types, []bool{false, false, true})
	var got [][]any
	for rows.Next() {
		var a, b, payload any
		if err := rows.Scan(&a, &b, &payload); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{a, b, payload})
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, want.rows) {
		t.Fatal("held eager rows changed", got, want.rows, rows.Err())
	}
}

func TestReferencePrimaryKeyTemporaryControls(t *testing.T) {
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
		run(referenceExec("CREATE TABLE persistent(a INT PRIMARY KEY)", 0))
		run(referenceExec("INSERT INTO persistent VALUES(7)", 1))
		run(referenceExec("CREATE TEMP TABLE retained_pk(a INT,b TEXT,PRIMARY KEY(a,b))", 0))
		run(referenceExec("INSERT INTO retained_pk VALUES(1,'x')", 1))
		referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec("CREATE TEMP TABLE retained_pk(a INT PRIMARY KEY)", 0))
		referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec("INSERT INTO retained_pk VALUES(81)", 1))
		var namespace string
		run(referenceExec("SET search_path=pg_temp,public", 0))
		if err := a.QueryRowContext(p.ctx, "SELECT current_schema").Scan(&namespace); err != nil || namespace == "" {
			t.Fatal(namespace, err)
		}
		run(referenceExec("SET search_path=public", 0))
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "retained_pk") || strings.Contains(dump.String(), "pg_temp_") {
				t.Fatal("TEMP PK leaked into Dump", dump.String())
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		run(referenceSelect("SELECT * FROM retained_pk", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}}))
		referenceRunStep(t, p.ctx, peer, b.recorder, referenceSelect("SELECT a FROM retained_pk", []string{"a"}, []string{"INT4"}, [][]any{{int64(81)}}))
		duplicate := uniqueReferenceDuplicate("retained_pk", "retained_pk_pkey", "Key (a, b)=(1, x) already exists.")
		duplicate.Schema = namespace
		run(referenceErrorDiagnostic("INSERT INTO retained_pk VALUES(1,'x')", duplicate))
		null := primaryKeyReferenceNull("retained_pk", "a", "null, x")
		null.Schema = namespace
		run(referenceErrorDiagnostic("INSERT INTO retained_pk VALUES(NULL,'x')", null))
		run(referenceSelect("SELECT a FROM persistent", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("DROP TABLE retained_pk", 0))
	})
}
