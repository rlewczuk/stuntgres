package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestUpdateCoreCompositionPredicates(t *testing.T) {
	cases := []struct {
		predicate string
		ids       []int64
	}{{"d IS NULL", []int64{2}}, {"d ISNULL", []int64{2}}, {"d IS NOT NULL", []int64{1, 3}}, {"d NOTNULL", []int64{1, 3}}, {"f IS TRUE", []int64{1}}, {"f IS FALSE", []int64{2}}, {"f IS UNKNOWN", []int64{3}}, {"f IS NOT TRUE", []int64{2, 3}}, {"b IS DISTINCT FROM sb", []int64{1}}, {"b IS NOT DISTINCT FROM sb", []int64{2, 3}}, {"b IS NOT DISTINCT FROM NULL", []int64{2}}, {"d IS DISTINCT FROM NULL", []int64{1, 3}}}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.predicate, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,f BOOL,d DATE,sd DATE,b BYTEA,sb BYTEA)")
				execSQL(t, db, `INSERT INTO t VALUES(1,10,TRUE,'2000-01-01','2000-02-29','\x41','\x4200'),(2,20,FALSE,NULL,NULL,NULL,NULL),(3,30,NULL,'2001-01-01','2004-02-29','\x41','\x41')`)
				q := "UPDATE t SET a=7 WHERE " + tc.predicate
				if prepared {
					stmt, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					for range 2 {
						r, err := stmt.Exec()
						if err != nil {
							t.Fatal(err)
						}
						n, err := r.RowsAffected()
						if err != nil || n != int64(len(tc.ids)) {
							t.Fatal(n, err)
						}
					}
				} else {
					r, err := db.Exec(q)
					if err != nil {
						t.Fatal(err)
					}
					n, err := r.RowsAffected()
					if err != nil || n != int64(len(tc.ids)) {
						t.Fatal(n, err)
					}
				}
				values := []int64{10, 20, 30}
				for _, id := range tc.ids {
					values[id-1] = 7
				}
				wantTuples(t, db, "SELECT id,a,f,d,sd,b,sb FROM t ORDER BY id", [][]any{{int64(1), values[0], true, "2000-01-01", "2000-02-29", []byte{65}, []byte{66, 0}}, {int64(2), values[1], false, nil, nil, nil, nil}, {int64(3), values[2], nil, "2001-01-01", "2004-02-29", []byte{65}, []byte{65}}})
			})
		}
	}
}

func TestUpdateCoreCompositionOldRowSwap(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,a INT,d DATE,sd DATE,b BYTEA,sb BYTEA)")
	execSQL(t, db, `INSERT INTO t VALUES(1,7,'2000-01-01','2000-02-29','\x01','\x005cff'),(2,NULL,NULL,NULL,NULL,NULL)`)
	q := "UPDATE t SET d=sd,sd=d,b=sb,sb=b"
	check := func(swapped bool) {
		d, sd := "2000-01-01", "2000-02-29"
		b, sb := []byte{1}, []byte{0, 92, 255}
		if swapped {
			d, sd = sd, d
			b, sb = sb, b
		}
		wantTuples(t, db, "SELECT id,d,sd,b,sb FROM t ORDER BY id", [][]any{{int64(1), d, sd, b, sb}, {int64(2), nil, nil, nil, nil}})
	}
	r, err := db.Exec(q)
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	check(true)
	held, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, swapped := range []bool{false, true} {
		r, err := held.Exec()
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != 2 {
			t.Fatal(n, err)
		}
		check(swapped)
	}
	for _, prepared := range []bool{false, true} {
		q := "UPDATE t SET a=CAST(NULL AS INTEGER),d=DATE '2000-02-29',b=NULL WHERE id=1"
		if prepared {
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			r, err = stmt.Exec()
			stmt.Close()
			if err != nil {
				t.Fatal(err)
			}
		} else {
			r, err = db.Exec(q)
			if err != nil {
				t.Fatal(err)
			}
		}
		n, err := r.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
		wantTuples(t, db, "SELECT a,d,b FROM t ORDER BY id", [][]any{{nil, "2000-02-29", nil}, {nil, nil, nil}})
	}
}
