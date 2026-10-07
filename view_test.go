package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestViewRowsPreparedAndMetadata(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{
		"CREATE TABLE t(id INT,txt TEXT,visible BOOL)",
		"INSERT INTO t VALUES(1,'a',true),(2,'b',false),(3,NULL,true),(3,NULL,true)",
		"CREATE VIEW v(k,label) AS SELECT id,txt FROM t WHERE visible",
		"CREATE VIEW w AS SELECT k,label FROM v WHERE k>1",
		"CREATE VIEW z AS SELECT FROM t",
	} {
		execSQL(t, db, query)
	}
	rows, err := db.QueryContext(ctx, "SELECT k,label FROM v ORDER BY k")
	queryCoreRows(t, rows, err, []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), nil}, {int64(3), nil}}, false)
	stmt, err := db.PrepareContext(ctx, "SELECT k,label FROM w WHERE k=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err = stmt.QueryContext(ctx, 3)
	queryCoreRows(t, rows, err, []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), nil}, {int64(3), nil}}, true)
	rows, err = stmt.QueryContext(ctx, nil)
	queryCoreRows(t, rows, err, []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{}, false)
	var count int64
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM z").Scan(&count); err != nil || count != 4 {
		t.Fatal(count, err)
	}
	execSQL(t, db, "INSERT INTO t VALUES(4,'d',true)")
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM v").Scan(&count); err != nil || count != 4 {
		t.Fatal(count, err)
	}
}

func TestViewIdentityDependenciesAndRestore(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{
		"CREATE TABLE t(unused INT,id INT,visible BOOL)",
		"INSERT INTO t VALUES(0,7,true)",
		"CREATE VIEW v(k) WITH(security_barrier=true) AS SELECT id FROM t WHERE visible",
		"CREATE VIEW w AS SELECT k FROM v",
		"ALTER TABLE t RENAME TO renamed",
		"ALTER TABLE renamed RENAME COLUMN id TO new_id",
		"ALTER TABLE renamed DROP COLUMN unused",
		"CREATE TABLE t(id INT)",
		"INSERT INTO t VALUES(99)",
	} {
		execSQL(t, db, query)
	}
	rows, err := db.QueryContext(ctx, "SELECT k FROM w")
	queryCoreRows(t, rows, err, []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
	_, err = db.ExecContext(ctx, "ALTER TABLE renamed DROP COLUMN visible")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "2BP01", Message: "cannot drop column visible of table renamed because other objects depend on it", Detail: "view v depends on column visible of table renamed\nview w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	restored := openLocal(t, stuntgres.NewDriver(), t.Name()+"-restored")
	if err = stuntgres.Restore(ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	rows, err = restored.QueryContext(ctx, "SELECT k FROM w")
	queryCoreRows(t, rows, err, []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
	execSQL(t, restored, "INSERT INTO renamed VALUES(8,true)")
	rows, err = restored.QueryContext(ctx, "SELECT k FROM w ORDER BY k")
	queryCoreRows(t, rows, err, []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}, false)
	if err = stuntgres.Restore(ctx, restored, bytes.NewBufferString("CREATE TABLE replacement(i INT); CREATE VIEW broken AS SELECT missing FROM replacement;")); err == nil {
		t.Fatal("invalid restore accepted")
	}
	rows, err = restored.QueryContext(ctx, "SELECT k FROM w ORDER BY k")
	queryCoreRows(t, rows, err, []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}, false)
}

func TestViewBarrier(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{"CREATE TABLE b(txt TEXT,visible BOOL)", "INSERT INTO b VALUES('1',true),('oops',false)", "CREATE VIEW safe WITH(security_barrier=true) AS SELECT txt FROM b WHERE visible", "CREATE VIEW nested AS SELECT txt FROM safe"} {
		execSQL(t, db, query)
	}
	rows, err := db.Query("SELECT CAST(txt AS INT) FROM nested WHERE CAST(txt AS INT)=1")
	queryCoreRows(t, rows, err, []string{"txt"}, []string{"INT4"}, [][]any{{int64(1)}}, false)
}

func TestViewCatalogMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "CREATE VIEW v AS SELECT id FROM t")
	rows, err := db.Query("SELECT oid,relname,relnamespace,relowner,relkind,relnatts,relhasrules FROM pg_catalog.pg_class WHERE relname='v'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"OID", "NAME", "OID", "OID", "CHAR", "INT2", "BOOL"}
	if len(types) != len(want) {
		t.Fatal(types)
	}
	for i := range want {
		if types[i].DatabaseTypeName() != want[i] {
			t.Fatalf("column %d: %s, want %s", i, types[i].DatabaseTypeName(), want[i])
		}
	}
	var oid, ns, owner int64
	var name, kind string
	var n int16
	var rules bool
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	if err = rows.Scan(&oid, &name, &ns, &owner, &kind, &n, &rules); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || ns <= 0 || owner <= 0 || name != "v" || kind != "v" || n != 1 || !rules || rows.Next() {
		t.Fatal(oid, ns, owner, name, kind, n, rules)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	rows, err = db.Query("SELECT rulename,ev_class,ev_type,ev_enabled,is_instead FROM pg_catalog.pg_rewrite WHERE ev_class=$1", oid)
	queryCoreRows(t, rows, err, []string{"rulename", "ev_class", "ev_type", "ev_enabled", "is_instead"}, []string{"NAME", "OID", "CHAR", "CHAR", "BOOL"}, [][]any{{"_RETURN", oid, "1", "O", true}}, false)
}
