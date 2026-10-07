package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func TestViewSmoke(t *testing.T) {
	ctx := context.Background()
	open := func(name string) *sql.DB {
		t.Helper()
		connector, err := stuntgres.NewDriver().OpenConnector(name)
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(connector)
		t.Cleanup(func() { db.Close() })
		return db
	}
	db := open(t.Name())
	exec := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(query, err)
		}
	}
	check := func(query string, names, types []string, want [][]any, args ...any) {
		t.Helper()
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			t.Fatal(query, err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, names) {
			t.Fatal(columns, names, err)
		}
		metadata, err := rows.ColumnTypes()
		if err != nil || len(metadata) != len(types) {
			t.Fatal(metadata, err)
		}
		for i, typ := range metadata {
			if typ.DatabaseTypeName() != types[i] {
				t.Fatal(i, typ.DatabaseTypeName(), types[i])
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			row := make([]any, len(columns))
			dst := make([]any, len(columns))
			for i := range row {
				dst[i] = &row[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if want == nil {
			want = make([][]any, 0)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(query, got, want)
		}
	}
	for _, query := range []string{
		"CREATE TABLE t(id INT,txt TEXT,visible BOOL)",
		"INSERT INTO t VALUES(1,'a',true),(2,'b',false),(3,NULL,true),(3,NULL,true)",
		"CREATE VIEW v(k,label) WITH(security_barrier=true) AS SELECT id,txt FROM t WHERE visible",
		"CREATE VIEW w AS SELECT k,label FROM v WHERE k>1",
		"CREATE VIEW z AS SELECT FROM t",
		"CREATE VIEW providers AS SELECT viewname FROM pg_catalog.pg_views",
	} {
		exec(query)
	}
	check("SELECT k,label FROM v ORDER BY k", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), nil}, {int64(3), nil}})
	check("SELECT count(*) FROM w", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})
	check("SELECT count(*) FROM z", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}})
	stmt, err := db.PrepareContext(ctx, "SELECT k,label FROM w WHERE k=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, arg := range []any{3, nil} {
		rows, err := stmt.QueryContext(ctx, arg)
		if err != nil {
			t.Fatal(err)
		}
		metadata, err := rows.ColumnTypes()
		if err != nil || len(metadata) != 2 || metadata[0].DatabaseTypeName() != "INT4" || metadata[1].DatabaseTypeName() != "TEXT" {
			t.Fatal(metadata, err)
		}
		count := 0
		for rows.Next() {
			var k int64
			var label sql.NullString
			if err := rows.Scan(&k, &label); err != nil || k != 3 || label.Valid {
				t.Fatal(k, label, err)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		want := 0
		if arg != nil {
			want = 2
		}
		if count != want {
			t.Fatal(count, want)
		}
	}
	for _, query := range []string{"CREATE TABLE b(txt TEXT,visible BOOL)", "INSERT INTO b VALUES('1',true),('oops',false)", "CREATE VIEW safe(txt,d,clock,stamp) WITH(security_barrier=true) AS SELECT txt,DATE '2000-01-02',TIME(3) '04:05:06.1235',TIMESTAMP(3) '2000-01-02 04:05:06.1235' FROM b WHERE visible"} {
		exec(query)
	}
	check("SELECT CAST(txt AS INT) FROM safe WHERE CAST(txt AS INT)=1", []string{"txt"}, []string{"INT4"}, [][]any{{int64(1)}})
	check("SELECT d,clock,stamp FROM safe", []string{"d", "clock", "stamp"}, []string{"DATE", "TIME", "TIMESTAMP"}, [][]any{{"2000-01-02", "04:05:06.124", time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC)}})
	exec("ALTER TABLE t RENAME TO renamed")
	exec("ALTER TABLE renamed RENAME COLUMN id TO new_id")
	exec("CREATE TABLE t(id INT)")
	exec("INSERT INTO t VALUES(99)")
	check("SELECT k FROM w ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}})
	_, err = db.ExecContext(ctx, "DROP TABLE renamed RESTRICT")
	var diagnostic *stuntgres.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "cannot drop table renamed because other objects depend on it" {
		t.Fatal(err)
	}
	var oid int64
	var kind string
	var rules bool
	if err = db.QueryRowContext(ctx, "SELECT oid,relkind,relhasrules FROM pg_catalog.pg_class WHERE relname='v'").Scan(&oid, &kind, &rules); err != nil || oid <= 0 || kind != "v" || !rules {
		t.Fatal(oid, kind, rules, err)
	}
	check("SELECT rulename,ev_class,ev_type,ev_enabled,is_instead FROM pg_catalog.pg_rewrite WHERE ev_class=$1", []string{"rulename", "ev_class", "ev_type", "ev_enabled", "is_instead"}, []string{"NAME", "OID", "CHAR", "CHAR", "BOOL"}, [][]any{{"_RETURN", oid, "1", "O", true}}, oid)
	var definition string
	if err = db.QueryRowContext(ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='v'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	exec("CREATE VIEW reconstructed AS " + definition)
	check("SELECT k FROM reconstructed ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(3)}})
	exec("INSERT INTO renamed VALUES(4,'d',true)")
	check("SELECT count(*) FROM v", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}})
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	db = open(t.Name() + "-restore")
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check("SELECT k FROM w ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}, {int64(4)}})
	check("SELECT d,clock,stamp FROM safe", []string{"d", "clock", "stamp"}, []string{"DATE", "TIME", "TIMESTAMP"}, [][]any{{"2000-01-02", "04:05:06.124", time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC)}})
	check("SELECT viewname FROM providers ORDER BY viewname", []string{"viewname"}, []string{"NAME"}, [][]any{{"providers"}, {"reconstructed"}, {"safe"}, {"v"}, {"w"}, {"z"}})
	if err = stuntgres.Restore(ctx, db, bytes.NewBufferString("CREATE TABLE lost(i INT); CREATE VIEW broken AS SELECT missing FROM lost;")); err == nil {
		t.Fatal("invalid restore accepted")
	}
	check("SELECT count(*) FROM v", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}})
	_, err = db.ExecContext(ctx, "DROP TABLE renamed RESTRICT")
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" {
		t.Fatal(err)
	}
}
