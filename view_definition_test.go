package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
	"time"
)

func TestViewDefinitionReconstructsOutputNames(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{"CREATE TABLE t(id INT,txt TEXT)", "INSERT INTO t VALUES(7,'seven')", "CREATE VIEW v(k,label) AS SELECT id,COALESCE(txt,'x') FROM t", "ALTER TABLE t RENAME COLUMN id TO renamed"} {
		execSQL(t, db, query)
	}
	var definition string
	if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname='v'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE VIEW reconstructed AS "+definition)
	rows, err := db.Query("SELECT k,label FROM reconstructed")
	queryCoreRows(t, rows, err, []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seven"}}, false)
}

func TestViewCanonicalProjectionLabels(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7)")
	rows, err := db.Query("SELECT id AS named FROM t")
	queryCoreRows(t, rows, err, []string{"named"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
	prepared, err := db.Prepare("SELECT id AS named FROM t WHERE id=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	rows, err = prepared.Query(7)
	queryCoreRows(t, rows, err, []string{"named"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
	execSQL(t, db, "CREATE VIEW v(k,label) AS SELECT id AS repeated,id AS repeated FROM t")
	rows, err = db.Query("SELECT k,label FROM v")
	queryCoreRows(t, rows, err, []string{"k", "label"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}, false)
	_, err = db.Exec("CREATE VIEW duplicate AS SELECT id AS repeated,id AS repeated FROM t")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42701", Message: `column "repeated" specified more than once`})
}

func TestViewIntegratedOIDNativeRoundTrip(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{
		"CREATE TABLE t(id OID)",
		"INSERT INTO t VALUES(1),('4294967295')",
		"CREATE VIEW v(id,high) AS SELECT id,CAST('-1' AS oid) FROM t WHERE id BETWEEN 1 AND CAST('-1' AS oid)",
		"CREATE VIEW w AS SELECT id,high FROM v",
		"CREATE TEMPORARY SEQUENCE retained",
	} {
		execSQL(t, db, query)
	}
	check := func() {
		rows, err := db.Query("SELECT id,high FROM w ORDER BY id")
		queryCoreRows(t, rows, err, []string{"id", "high"}, []string{"OID", "OID"}, [][]any{{int64(1), int64(4294967295)}, {int64(4294967295), int64(4294967295)}}, false)
		rows, err = db.Query("SELECT id FROM w WHERE id=$1", int64(-1))
		queryCoreRows(t, rows, err, []string{"id"}, []string{"OID"}, [][]any{{int64(4294967295)}}, false)
	}
	check()
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestViewIntegratedBetweenDefinitionRoundTrip(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(2)")
	execSQL(t, db, "CREATE VIEW v(matches) AS SELECT TRUE BETWEEN (id BETWEEN 1 AND 3) AND TRUE FROM t")
	check := func(name string) {
		rows, err := db.Query("SELECT matches FROM " + name)
		queryCoreRows(t, rows, err, []string{"matches"}, []string{"BOOL"}, [][]any{{true}}, false)
	}
	check("v")
	var definition string
	if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname='v'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE VIEW reconstructed AS "+definition)
	check("reconstructed")
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check("v")
	check("reconstructed")
}

func TestViewAnalyzedTemporalLiteralPersistence(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,d DATE,ts TIMESTAMP)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'2000-02-01','2000-02-02'),(2,'2000-01-02','2000-01-03')")
	execSQL(t, db, "CREATE VIEW literals(d,clock,stamp) AS SELECT DATE '2000-01-02',TIME(3) '04:05:06.1235',TIMESTAMP(3) '2000-01-02 04:05:06.1235' FROM t WHERE id=1")
	execSQL(t, db, "CREATE VIEW contextual(id) AS SELECT id FROM t WHERE d='2000-02-01'")
	execSQL(t, db, "CREATE VIEW repeated(matches) AS SELECT '2000-02-01' BETWEEN d AND ts FROM t WHERE id=1")
	check := func(name, suffix string) {
		switch name {
		case "literals":
			rows, err := db.Query("SELECT d,clock,stamp FROM " + name + suffix)
			queryCoreRows(t, rows, err, []string{"d", "clock", "stamp"}, []string{"DATE", "TIME", "TIMESTAMP"}, [][]any{{"2000-01-02", "04:05:06.124", time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC)}}, false)
		case "contextual":
			rows, err := db.Query("SELECT id FROM " + name + suffix)
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, false)
		case "repeated":
			rows, err := db.Query("SELECT matches FROM " + name + suffix)
			queryCoreRows(t, rows, err, []string{"matches"}, []string{"BOOL"}, [][]any{{true}}, false)
		}
	}
	for _, name := range []string{"literals", "contextual", "repeated"} {
		check(name, "")
		var definition string
		if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", name).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		execSQL(t, db, "CREATE VIEW "+name+"_copy AS "+definition)
		check(name, "_copy")
	}
	execSQL(t, db, "CREATE TEMPORARY SEQUENCE retained")
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"literals", "contextual", "repeated"} {
		check(name, "")
		check(name, "_copy")
	}
}

func TestViewLiteralOccurrencesBeforeGrouping(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{
		"CREATE TABLE t(d DATE,txt TEXT)",
		"INSERT INTO t VALUES('2000-02-01','bad')",
		"CREATE VIEW grouped(clock,n) AS SELECT TIME(3) '04:05:06.1235',COUNT(*) FROM t WHERE d=DATE '2000-02-01' GROUP BY TIME(3) '04:05:06.1235' HAVING DATE '2000-02-01'=DATE '2000-02-01'",
		"CREATE VIEW branches(matches,safe) AS SELECT CASE DATE '2000-02-01' WHEN d THEN true ELSE false END,CASE WHEN d=DATE '2000-02-01' THEN 7 ELSE CAST(txt AS int4) END FROM t",
	} {
		execSQL(t, db, query)
	}
	check := func(suffix string) {
		rows, err := db.Query("SELECT clock,n FROM grouped" + suffix)
		queryCoreRows(t, rows, err, []string{"clock", "n"}, []string{"TIME", "INT8"}, [][]any{{"04:05:06.124", int64(1)}}, false)
		rows, err = db.Query("SELECT matches,safe FROM branches" + suffix)
		queryCoreRows(t, rows, err, []string{"matches", "safe"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(7)}}, false)
	}
	check("")
	for _, name := range []string{"grouped", "branches"} {
		var definition string
		if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", name).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		execSQL(t, db, "CREATE VIEW "+name+"_copy AS "+definition)
	}
	check("_copy")
	execSQL(t, db, "CREATE TEMPORARY SEQUENCE retained")
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check("")
	check("_copy")
}

func TestViewTemporalEndpointAndTypedNullPersistence(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	execSQL(t, db, "CREATE VIEW endpoints(day,clock,stamp,optional) AS SELECT DATE 'infinity',TIME(0) '24:00:00',TIMESTAMP(0) '294276-12-31 23:59:59.999999',CASE WHEN id=0 THEN DATE '2000-01-01' ELSE NULL END FROM t")
	check := func(name string) {
		rows, err := db.Query("SELECT day,clock,stamp,optional FROM " + name)
		queryCoreRows(t, rows, err, []string{"day", "clock", "stamp", "optional"}, []string{"DATE", "TIME", "TIMESTAMP", "DATE"}, [][]any{{"infinity", "24:00:00", time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC), nil}}, false)
	}
	check("endpoints")
	var definition string
	if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname='endpoints'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE VIEW endpoints_copy AS "+definition)
	check("endpoints_copy")
	execSQL(t, db, "CREATE TEMPORARY SEQUENCE retained")
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check("endpoints")
	check("endpoints_copy")
}

func TestViewContextualScalarLiteralNativeRoundTrip(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{
		"CREATE TABLE t(id INT,n NAME,u UUID,b BYTEA,num NUMERIC(5,2))",
		"INSERT INTO t(id) VALUES(1)",
		`CREATE VIEW contextual(n,u,b,num) AS SELECT COALESCE(n,'MiXeD'),COALESCE(u,'550e8400-e29b-41d4-a716-446655440000'),COALESCE(b,'\x61'),COALESCE(num,'1.24') FROM t WHERE id=1`,
		"CREATE VIEW numeric_scale(value) AS SELECT 1e3 FROM t WHERE id=1",
	} {
		execSQL(t, db, query)
	}
	check := func(suffix string) {
		t.Helper()
		for _, tc := range []struct {
			name, projection string
			names, types     []string
			want             [][]any
		}{
			{"contextual", "n,u,b,num", []string{"n", "u", "b", "num"}, []string{"NAME", "UUID", "BYTEA", "NUMERIC"}, [][]any{{"MiXeD", "550e8400-e29b-41d4-a716-446655440000", []byte("a"), "1.24"}}},
			{"numeric_scale", "value", []string{"value"}, []string{"NUMERIC"}, [][]any{{"1000"}}},
		} {
			query := "SELECT " + tc.projection + " FROM " + tc.name + suffix
			rows, err := db.Query(query)
			queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
			prepared, err := db.Prepare(query)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = prepared.Query()
			queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
			if err := prepared.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	check("")
	for _, name := range []string{"contextual", "numeric_scale"} {
		var definition string
		if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", name).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		execSQL(t, db, "CREATE VIEW "+name+"_copy AS "+definition)
	}
	check("_copy")
	// Retained TEMP provenance forces imported literals to use the destination
	// builtin generation, rather than only exercising a fresh catalog's types.
	execSQL(t, db, "CREATE TEMPORARY SEQUENCE retained")
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check("")
	check("_copy")
}

func TestViewCatalogCHARLiteralNativeRoundTrip(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE char_source(id INT)")
	execSQL(t, db, "CREATE VIEW char_literal AS SELECT relkind < 'é' AS below FROM pg_catalog.pg_class WHERE relname='char_source'")
	prepared, err := db.Prepare("SELECT below FROM char_literal")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	check := func(name string, statement *sql.Stmt) {
		t.Helper()
		rows, err := db.Query("SELECT below FROM " + name)
		queryCoreRows(t, rows, err, []string{"below"}, []string{"BOOL"}, [][]any{{true}}, false)
		rows, err = statement.Query()
		queryCoreRows(t, rows, err, []string{"below"}, []string{"BOOL"}, [][]any{{true}}, false)
	}
	check("char_literal", prepared)
	var definition string
	if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname='char_literal'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE VIEW reconstructed AS "+definition)
	reconstructed, err := db.Prepare("SELECT below FROM reconstructed")
	if err != nil {
		t.Fatal(err)
	}
	defer reconstructed.Close()
	check("char_literal", prepared)
	check("reconstructed", reconstructed)
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	// Reuse both prepared handles across native catalog replacement.
	check("char_literal", prepared)
	check("reconstructed", reconstructed)
}
