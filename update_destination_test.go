package stuntgres_test

import (
	"stuntgres"
	"testing"
	"time"
)

func TestUpdateCoreDestinationMatrix(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,src INT,s TEXT,u UUID,tm TIME(2),ts TIMESTAMP(2),n NUMERIC(3,1),fn NUMERIC)")
	execSQL(t, db, "INSERT INTO t VALUES(1,7,'old',NULL,NULL,NULL,1.0,1.25),(2,-7,'old',NULL,NULL,NULL,1.0,-1.25)")
	q := "UPDATE t SET s=src,u=$1,tm=$2,ts=$3,n=fn"
	uuid := "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
	check := func(u, clock, stamp any) {
		wantTuples(t, db, "SELECT id,s,u,tm,ts,n FROM t ORDER BY id", [][]any{{int64(1), "7", u, clock, stamp, "1.3"}, {int64(2), "-7", u, clock, stamp, "-1.3"}})
	}
	args := []any{uuid, "04:05:06.126", "2024-01-02 03:04:05.126"}
	result, err := db.Exec(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	clock := "04:05:06.13"
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 130000000, time.UTC)
	check(uuid, clock, stamp)
	stmt, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err = stmt.Exec(nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	check(nil, nil, nil)
	if _, err = stmt.Exec(args...); err != nil {
		t.Fatal(err)
	}
	check(uuid, clock, stamp)
	execSQL(t, db, "UPDATE t SET fn=99.95 WHERE id=2")
	late, err := db.Prepare("UPDATE t SET n=fn,s='discard'")
	if err != nil {
		t.Fatal(err)
	}
	defer late.Close()
	for _, prepared := range []bool{false, true} {
		if prepared {
			_, err = late.Exec()
		} else {
			_, err = db.Exec("UPDATE t SET n=fn,s='discard'")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."})
		check(uuid, clock, stamp)
	}
}
