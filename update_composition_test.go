package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateCoreComposition(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(id INT,d DATE,b BYTEA,srcd DATE,srcb BYTEA,n INT NOT NULL,f BOOL)")
	execSQL(t, db, `INSERT INTO t VALUES(1,'2000-01-01','\x0102','2024-02-29','\x005cff',1,TRUE),(2,'2000-01-02','\x03',NULL,NULL,1,NULL)`)
	check := func(date, blob any, flag1, flag2 bool) {
		wantTuples(t, db, "SELECT id,d,b,n,f FROM t ORDER BY id", [][]any{{int64(1), date, blob, int64(1), flag1}, {int64(2), nil, nil, int64(1), flag2}})
	}
	affected := func(q string, want int64, args ...any) {
		r, err := db.Exec(q, args...)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != want {
			t.Fatal(n, err)
		}
	}
	affected("UPDATE t SET d=srcd,b=srcb,f=d IS NOT NULL WHERE b IS DISTINCT FROM srcb", 2)
	check("2024-02-29", []byte{0, 92, 255}, true, true)
	old, err := db.Prepare("UPDATE t SET d=srcd,b=srcb,f=d IS NOT NULL")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	for range 2 {
		r, err := old.Exec()
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 2 {
			t.Fatal(n, err)
		}
		check("2024-02-29", []byte{0, 92, 255}, true, false)
	}
	q := "UPDATE t SET n=CAST(n AS BIGINT)::INTEGER,f=(b IS NULL) IS FALSE WHERE d IS NOT DISTINCT FROM srcd"
	affected(q, 2)
	casts, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer casts.Close()
	if _, err = casts.Exec(); err != nil {
		t.Fatal(err)
	}
	check("2024-02-29", []byte{0, 92, 255}, true, false)
	affected("UPDATE t SET f=(d IS NULL) IS TRUE WHERE f IS NOT UNKNOWN", 2)
	check("2024-02-29", []byte{0, 92, 255}, false, true)
	q = "UPDATE t SET d=$1,b=$2 WHERE id=$3"
	affected(q, 1, "2030-01-02", []byte{65, 0, 255}, 1)
	check("2030-01-02", []byte{65, 0, 255}, false, true)
	affected(q, 1, nil, nil, 1)
	check(nil, nil, false, true)
	held, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err = held.Exec(nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	check(nil, nil, false, true)
	if _, err = held.Exec("2031-02-03", `\q`, 1); err != nil {
		t.Fatal("BYTEA string Bind is raw bytes", err)
	}
	check("2031-02-03", []byte{92, 113}, false, true)
	if _, err = held.Exec("2031-02-03", `\x41`, 1); err != nil {
		t.Fatal(err)
	}
	check("2031-02-03", []byte{92, 120, 52, 49}, false, true)
	if _, err = held.Exec("2031-02-03", []byte{}, 1); err != nil {
		t.Fatal(err)
	}
	check("2031-02-03", []byte{}, false, true)
	payload := []byte{9, 92, 128}
	if _, err = held.Exec("2031-02-03", payload, 1); err != nil {
		t.Fatal(err)
	}
	payload[0] = 99
	check("2031-02-03", []byte{9, 92, 128}, false, true)
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{{"UPDATE t SET d='bad'", stuntgres.Error{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "16"}}, {"UPDATE t SET d='1900-02-29'", stuntgres.Error{Code: "22008", Message: `date/time field value out of range: "1900-02-29"`, Position: "16"}}, {`UPDATE t SET b='\x0'`, stuntgres.Error{Code: "22023", Message: "invalid hexadecimal data: odd number of digits", Position: "16"}}, {`UPDATE t SET b='\xGG'`, stuntgres.Error{Code: "22023", Message: `invalid hexadecimal digit: "G"`, Position: "16"}}, {`UPDATE t SET b='\q'`, stuntgres.Error{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "16"}}} {
		for _, prefix := range []string{"", "/*é*/ "} {
			want := tc.want
			if prefix != "" {
				want.Position = "22"
			}
			q := prefix + tc.q
			_, err := db.Exec(q)
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid literal prepared")
			}
			typeCoreDiagnostic(t, err, want)
			check("2031-02-03", []byte{9, 92, 128}, false, true)
		}
	}
	for _, prepared := range []bool{false, true} {
		var err error
		if prepared {
			_, err = held.Exec("bad", []byte{42}, 1)
		} else {
			_, err = db.Exec(q, "bad", []byte{42}, 1)
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type date: "bad"`})
		check("2031-02-03", []byte{9, 92, 128}, false, true)
	}
	execSQL(t, db, "CREATE TABLE nn(id INT,d DATE,b BYTEA,n INT NOT NULL)")
	execSQL(t, db, `INSERT INTO nn VALUES(1,'2000-01-01','\x01',1)`)
	failed, err := db.Prepare("UPDATE nn SET b=$1,d=$2,n=NULL WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer failed.Close()
	for _, output := range []struct{ name, detail string }{{"hex", `Failing row contains (1, 2024-02-29, \x005cff, null).`}, {"escape", `Failing row contains (1, 2024-02-29, \000\\\377, null).`}} {
		execSQL(t, db, "SET bytea_output="+output.name)
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				_, err = failed.Exec([]byte{0, 92, 255}, "2024-02-29")
			} else {
				_, err = db.Exec("UPDATE nn SET b=$1,d=$2,n=NULL WHERE id=1", []byte{0, 92, 255}, "2024-02-29")
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "n" of relation "nn" violates not-null constraint`, Detail: output.detail, Schema: "public", Table: "nn", Column: "n"})
		}
		wantTuples(t, db, "SELECT id,d,b,n FROM nn", [][]any{{int64(1), "2000-01-01", []byte{1}, int64(1)}})
	}
}

func TestUpdateCoreCompositionAtomicNotNull(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(id INT,d DATE,b BYTEA,n INT NOT NULL,nextn INT)")
	execSQL(t, db, `INSERT INTO t VALUES(1,'2000-01-01','\x01',1,2),(2,'2000-01-01','\x02',1,NULL)`)
	q := "UPDATE t SET d=$1,b=$2,n=nextn"
	held, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	check := func() {
		wantTuples(t, db, "SELECT id,d,b,n,nextn FROM t ORDER BY id", [][]any{{int64(1), "2000-01-01", []byte{1}, int64(1), int64(2)}, {int64(2), "2000-01-01", []byte{2}, int64(1), nil}})
	}
	for _, prepared := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			if explicit {
				execSQL(t, db, "BEGIN")
				execSQL(t, db, "UPDATE t SET n=7 WHERE id=1")
			}
			var err error
			if prepared {
				_, err = held.Exec("2024-02-29", []byte{0, 92, 255})
			} else {
				_, err = db.Exec(q, "2024-02-29", []byte{0, 92, 255})
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: `Failing row contains (2, 2024-02-29, \x005cff, null, null).`, Schema: "public", Table: "t", Column: "n"})
			if explicit {
				rows, err := db.Query("SELECT n FROM t")
				if rows != nil {
					rows.Close()
					t.Fatal("aborted transaction exposed rows")
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
				execSQL(t, db, "ROLLBACK")
			}
			check()
		}
	}
}

func TestUpdateCoreCompositionArithmetic(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			db.SetMaxOpenConns(2)
			execSQL(t, db, "CREATE TABLE t(id INT,a INT,b INT)")
			execSQL(t, db, "INSERT INTO t VALUES(1,7,2),(1,7,2),(2,NULL,4)")
			old, err := db.Query("SELECT id,a,b FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			q := "UPDATE t SET a=a+b,b=a-b WHERE id+$1=$2"
			execute := func(args ...any) (int64, error) {
				r, err := db.Exec(q, args...)
				if err != nil {
					return 0, err
				}
				return r.RowsAffected()
			}
			if prepared {
				held, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				execute = func(args ...any) (int64, error) {
					r, err := held.Exec(args...)
					if err != nil {
						return 0, err
					}
					return r.RowsAffected()
				}
			}
			types := []string{"INT4", "INT4", "INT4"}
			check := func(want [][]any) {
				arithmeticRows(t, db, "SELECT id,a,b FROM t ORDER BY id", types, want)
			}
			for _, frame := range []struct {
				args []any
				n    int64
				rows [][]any
			}{
				{[]any{1, 2}, 2, [][]any{{int64(1), int64(9), int64(5)}, {int64(1), int64(9), int64(5)}, {int64(2), nil, int64(4)}}},
				{[]any{nil, 2}, 0, [][]any{{int64(1), int64(9), int64(5)}, {int64(1), int64(9), int64(5)}, {int64(2), nil, int64(4)}}},
				{[]any{0, 1}, 2, [][]any{{int64(1), int64(14), int64(4)}, {int64(1), int64(14), int64(4)}, {int64(2), nil, int64(4)}}},
				{[]any{0, 2}, 1, [][]any{{int64(1), int64(14), int64(4)}, {int64(1), int64(14), int64(4)}, {int64(2), nil, nil}}},
			} {
				if n, err := execute(frame.args...); err != nil || n != frame.n {
					t.Fatal(n, err, frame)
				}
				check(frame.rows)
			}
			arithmeticRows(t, db, "SELECT id,a,b FROM t WHERE FALSE", types, [][]any{})
			compareOpsRows(t, old, nil, types, [][]any{{int64(1), int64(7), int64(2)}, {int64(1), int64(7), int64(2)}, {int64(2), nil, int64(4)}})
		})
	}
}

func TestUpdateCoreCompositionArithmeticAtomicity(t *testing.T) {
	for _, tc := range []struct {
		input, q, code, message string
		rows                    [][]any
	}{
		{"(1,7,2),(2,0,4),(3,NULL,6)", "UPDATE t SET a=14/a,b=b+1", "22012", "division by zero", [][]any{{int64(1), int64(7), int64(2)}, {int64(2), int64(0), int64(4)}, {int64(3), nil, int64(6)}}},
		{"(1,1,2),(2,2147483647,4)", "UPDATE t SET a=a+1,b=b+1", "22003", "integer out of range", [][]any{{int64(1), int64(1), int64(2)}, {int64(2), int64(2147483647), int64(4)}}},
	} {
		for _, prepared := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(tc.code+map[bool]string{false: "/direct", true: "/prepared"}[prepared]+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					db.SetMaxOpenConns(1)
					execSQL(t, db, "CREATE TABLE t(id INT,a INT,b INT)")
					execSQL(t, db, "INSERT INTO t VALUES"+tc.input)
					execute := func() error {
						r, err := db.Exec(tc.q)
						if r != nil {
							t.Fatal("failed UPDATE returned a result")
						}
						return err
					}
					if prepared {
						held, err := db.Prepare(tc.q)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						execute = func() error {
							r, err := held.Exec()
							if r != nil {
								t.Fatal("failed prepared UPDATE returned a result")
							}
							return err
						}
					}
					if explicit {
						execSQL(t, db, "BEGIN")
						execSQL(t, db, "UPDATE t SET b=99 WHERE id=1")
					}
					typeCoreDiagnostic(t, execute(), stuntgres.Error{Code: tc.code, Message: tc.message})
					if explicit {
						_, err := db.Exec("UPDATE t SET b=0")
						typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
						execSQL(t, db, "ROLLBACK")
					}
					arithmeticRows(t, db, "SELECT id,a,b FROM t ORDER BY id", []string{"INT4", "INT4", "INT4"}, tc.rows)
				})
			}
		}
	}
}
