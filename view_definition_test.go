package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
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
