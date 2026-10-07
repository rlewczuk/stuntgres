package stuntgres_test

import (
	"reflect"
	"testing"

	"stuntgres"
)

func TestOIDMixedDistinct(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE one(id INT,s SMALLINT,i INT,b BIGINT,v OID)")
	execSQL(t, db, "INSERT INTO one VALUES(1,-1,-1,4294967295,'4294967295'),(2,NULL,NULL,NULL,NULL),(3,-1,-1,4294967295,NULL),(4,1,1,1,'0'),(5,NULL,NULL,NULL,'4294967295')")
	q := "SELECT v IS DISTINCT FROM s,s IS DISTINCT FROM v,v IS NOT DISTINCT FROM s,s IS NOT DISTINCT FROM v,v IS DISTINCT FROM i,i IS DISTINCT FROM v,v IS NOT DISTINCT FROM i,i IS NOT DISTINCT FROM v,v IS DISTINCT FROM b,b IS DISTINCT FROM v,v IS NOT DISTINCT FROM b,b IS NOT DISTINCT FROM v FROM one ORDER BY id"
	want := [][]any{
		{false, false, true, true, false, false, true, true, false, false, true, true},
		{false, false, true, true, false, false, true, true, false, false, true, true},
		{true, true, false, false, true, true, false, false, true, true, false, false},
		{true, true, false, false, true, true, false, false, true, true, false, false},
		{true, true, false, false, true, true, false, false, true, true, false, false},
	}
	wantTuples(t, db, q, want)
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.Query()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "BOOL" || typ.ScanType() != reflect.TypeFor[bool]() {
			t.Fatal(typ)
		}
	}
	var got [][]any
	for rows.Next() {
		row, destinations := make([]any, 12), make([]any, 12)
		for i := range row {
			destinations[i] = &row[i]
		}
		if err = rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
	for _, value := range []string{"(-1)::int8", "4294967296::int8"} {
		for _, kind := range []string{" IS DISTINCT FROM ", " IS NOT DISTINCT FROM "} {
			for _, q := range []string{"SELECT v" + kind + value + " FROM one", "SELECT " + value + kind + "v FROM one"} {
				_, err := db.Exec(q)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "OID out of range"})
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(q, err)
				}
				_, err = stmt.Exec()
				stmt.Close()
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "OID out of range"})
			}
		}
	}
	inferred, err := db.Prepare("SELECT v IS DISTINCT FROM $1,$1 IS NOT DISTINCT FROM v FROM one WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer inferred.Close()
	for _, tc := range []struct {
		value          any
		distinct, same bool
	}{{"4294967295", false, true}, {nil, true, false}, {"0", true, false}} {
		var distinct, same bool
		if err = inferred.QueryRow(tc.value).Scan(&distinct, &same); err != nil || distinct != tc.distinct || same != tc.same {
			t.Fatal(distinct, same, tc, err)
		}
	}
	wantTuples(t, db, "SELECT NULL::oid IS DISTINCT FROM s,s IS NOT DISTINCT FROM NULL::oid,NULL::oid IS DISTINCT FROM i,i IS NOT DISTINCT FROM NULL::oid,NULL::oid IS DISTINCT FROM b,b IS NOT DISTINCT FROM NULL::oid,v=NULL::oid FROM one WHERE id=2", [][]any{{false, true, false, true, false, true, nil}})
}
