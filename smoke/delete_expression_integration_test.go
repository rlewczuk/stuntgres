package smoke_test

import (
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
)

func TestDeleteCoreIntegratedExpressionsSmoke(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(query string, count int64) {
		t.Helper()
		result, err := db.Exec(query)
		if err != nil {
			t.Fatal(query, err)
		}
		if got, err := result.RowsAffected(); err != nil || got != count {
			t.Fatal(query, got, err)
		}
	}
	exec("CREATE TABLE t(id INT,v INT,f BOOL)", 0)
	exec("INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE)", 3)
	stmt, err := db.Prepare("DELETE FROM t AS d WHERE d.v IS NOT DISTINCT FROM $1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	result, err := stmt.Exec(nil)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	result, err = db.Exec("DELETE FROM t WHERE FALSE AND int4($1::text)=id", "bad")
	var diagnostic *stuntgres.Error
	if result != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Message != `invalid input syntax for type integer: "bad"` {
		t.Fatal("decisive predicate masked conversion failure", result, err)
	}
	for _, query := range []string{"DELETE FROM t d WHERE d.v::int4=10", "DELETE FROM t d WHERE CAST(d.v AS int4)=10", "DELETE FROM t d WHERE int4(d.v)=10"} {
		if result, err := db.Exec(query); result != nil || err == nil {
			t.Fatal("qualified cast executed", query, result, err)
		}
	}
	exec("DELETE FROM t AS d WHERE d.f IS FALSE AND CAST(v AS BIGINT)=20", 1)
	exec("DELETE FROM t AS d WHERE d.v ISNULL", 0)
	exec("DELETE FROM t AS d WHERE d.f IS TRUE AND v::int8=99", 0)
	exec("DELETE FROM t WHERE int8(v)=99", 0)
	rows, err := db.Query("SELECT id FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing survivor", rows.Err())
	}
	var id int64
	if err := rows.Scan(&id); err != nil || id != 1 {
		t.Fatal(id, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected survivor", rows.Err())
	}
	exec("CREATE TABLE frames(v INT)", 0)
	exec("INSERT INTO frames VALUES(10),(NULL),(20)", 3)
	held, err := db.Prepare("DELETE FROM frames WHERE v IS NOT DISTINCT FROM int4($1::text)")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, call := range []struct {
		arg       any
		bad       bool
		remaining int64
	}{{"bad", true, 3}, {"10", false, 2}, {nil, false, 1}, {"bad", true, 1}, {"20", false, 0}} {
		result, err := held.Exec(call.arg)
		if call.bad {
			if result != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" {
				t.Fatal(result, err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if count, err := result.RowsAffected(); err != nil || count != 1 {
				t.Fatal(count, err)
			}
		}
		rows, err := db.Query("SELECT v FROM frames")
		if err != nil {
			t.Fatal(err)
		}
		var count int64
		for rows.Next() {
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil || count != call.remaining {
			t.Fatal(count, err, call.remaining)
		}
	}
}
