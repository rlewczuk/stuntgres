package stuntgres_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

var deparseReviewConditionalCases = []struct {
	name, input, compact, pretty, good, bad, assignment, detail string
	rows                                                        [][]any
	nullReject                                                  bool
}{
	{"searched_case", "CASE WHEN flag THEN a ELSE 2 END>0", "(\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0)", "\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0", "(1,1,1,true),(2,0,1,false),(3,0,1,NULL),(4,NULL,1,true)", "0,1,true", "a=0,flag=true", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(0), int64(1), false}, {int64(3), int64(0), int64(1), nil}, {int64(4), nil, int64(1), true}}, false},
	{"simple_case", "CASE a WHEN 1 THEN true ELSE false END", "\nCASE a\n    WHEN 1 THEN true\n    ELSE false\nEND", "\nCASE a\n    WHEN 1 THEN true\n    ELSE false\nEND", "(1,1,1,true),(2,1,2,false)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(1), int64(2), false}}, true},
	{"null_arm", "CASE WHEN flag THEN a END>0", "(\nCASE\n    WHEN flag THEN a\n    ELSE NULL::integer\nEND > 0)", "\nCASE\n    WHEN flag THEN a\n    ELSE NULL::integer\nEND > 0", "(1,1,1,true),(2,0,1,false),(3,0,1,NULL),(4,NULL,1,true)", "0,1,true", "a=0,flag=true", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(0), int64(1), false}, {int64(3), int64(0), int64(1), nil}, {int64(4), nil, int64(1), true}}, false},
	{"coalesce", "COALESCE(a,1)>0", "(COALESCE(a, 1) > 0)", "COALESCE(a, 1) > 0", "(1,1,1,true),(2,NULL,1,true)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), nil, int64(1), true}}, false},
	{"nullif", "NULLIF(a,1)>0", "(NULLIF(a, 1) > 0)", "NULLIF(a, 1) > 0", "(1,2,1,true),(2,1,1,true),(3,NULL,1,true)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(2), int64(1), true}, {int64(2), int64(1), int64(1), true}, {int64(3), nil, int64(1), true}}, false},
	{"greatest", "GREATEST(a,-1)>0", "(GREATEST(a, '-1'::integer) > 0)", "GREATEST(a, '-1'::integer) > 0", "(1,1,1,true),(2,2,1,true)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(2), int64(1), true}}, true},
	{"least", "LEAST(a,3)>0", "(LEAST(a, 3) > 0)", "LEAST(a, 3) > 0", "(1,1,1,true),(2,4,1,true),(3,NULL,1,true)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(4), int64(1), true}, {int64(3), nil, int64(1), true}}, false},
	{"arithmetic", "(a+1)*2>0", "(((a + 1) * 2) > 0)", "((a + 1) * 2) > 0", "(1,0,1,true),(2,1,1,true),(3,NULL,1,true)", "-1,1,true", "a=-1", "-1, 1, t", [][]any{{int64(1), int64(0), int64(1), true}, {int64(2), int64(1), int64(1), true}, {int64(3), nil, int64(1), true}}, false},
	{"divide_subtract", "(-a+(b-1)*2)/2>0", "((((- a) + ((b - 1) * 2)) / 2) > 0)", "(((- a) + (b - 1) * 2) / 2) > 0", "(1,-2,1,true),(2,-4,1,true),(3,NULL,1,true),(4,1,NULL,true)", "2,1,true", "a=2", "2, 1, t", [][]any{{int64(1), int64(-2), int64(1), true}, {int64(2), int64(-4), int64(1), true}, {int64(3), nil, int64(1), true}, {int64(4), int64(1), nil, true}}, false},
	{"remainder", "a%3=1", "((a % 3) = 1)", "(a % 3) = 1", "(1,1,1,true),(2,4,1,true),(3,NULL,1,true)", "2,1,true", "a=2", "2, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(4), int64(1), true}, {int64(3), nil, int64(1), true}}, false},
	{"bit_case", "CASE WHEN flag THEN (a&b) ELSE 1 END>0", "(\nCASE\n    WHEN flag THEN (a & b)\n    ELSE 1\nEND > 0)", "\nCASE\n    WHEN flag THEN a & b\n    ELSE 1\nEND > 0", "(1,1,1,true),(2,0,1,false),(3,NULL,1,true)", "0,1,true", "a=0,flag=true", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(0), int64(1), false}, {int64(3), nil, int64(1), true}}, false},
	{"bit_func", "abs(a&b)>0", "(abs((a & b)) > 0)", "abs(a & b) > 0", "(1,1,1,true),(2,3,1,true),(3,NULL,1,true)", "0,1,true", "a=0", "0, 1, t", [][]any{{int64(1), int64(1), int64(1), true}, {int64(2), int64(3), int64(1), true}, {int64(3), nil, int64(1), true}}, false},
}

func TestDeparseConstraintsReviewConditionalConsumers(t *testing.T) {
	for _, tc := range deparseReviewConditionalCases {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE original(id INT,a INT,b INT,flag BOOL,CONSTRAINT c CHECK("+tc.input+"))")
			for mode, expression := range []string{tc.compact, tc.pretty} {
				q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
				deparseReviewTypedValue(t, db, q, "TEXT", "CHECK ("+expression+")")
				var actual string
				if err := db.QueryRow(q).Scan(&actual); err != nil {
					t.Fatal(err)
				}
				execSQL(t, db, fmt.Sprintf("CREATE TABLE copy%d(id INT,a INT,b INT,flag BOOL,CONSTRAINT c%d %s)", mode, mode, actual))
			}
			for i, name := range []string{"original", "copy0", "copy1"} {
				constraint := "c"
				if i > 0 {
					constraint = fmt.Sprintf("c%d", i-1)
				}
				for _, prepared := range []bool{false, true} {
					execSQL(t, db, "DELETE FROM "+name)
					tableCoreExec(t, db, prepared, "INSERT INTO "+name+" VALUES"+tc.good, int64(len(tc.rows)))
					for _, op := range []struct{ q, id string }{
						{"INSERT INTO " + name + " VALUES(9," + tc.bad + ")", "9"},
						{"INSERT INTO " + name + " VALUES" + strings.Replace(strings.Split(tc.good, ")")[0]+")", "(1,", "(8,", 1) + ",(9," + tc.bad + ")," + strings.Replace(strings.Split(tc.good, ")")[0]+")", "(1,", "(10,", 1), "9"},
						{"UPDATE " + name + " SET " + tc.assignment + " WHERE id=1", "1"},
					} {
						checkExecError(t, db, prepared, op.q, checkViolation(name, constraint, op.id+", "+tc.detail))
						wantTuples(t, db, "SELECT id,a,b,flag FROM "+name+" ORDER BY id", tc.rows)
					}
					extra := map[string]string{"simple_case": "2", "coalesce": "-1", "nullif": "-1", "greatest": "-2", "least": "-1", "arithmetic": "-2", "remainder": "-2"}[tc.name]
					if extra != "" {
						checkExecError(t, db, prepared, "INSERT INTO "+name+" VALUES(9,"+extra+",1,true)", checkViolation(name, constraint, "9, "+extra+", 1, t"))
						wantTuples(t, db, "SELECT id,a,b,flag FROM "+name+" ORDER BY id", tc.rows)
					}
					if tc.nullReject {
						checkExecError(t, db, prepared, "INSERT INTO "+name+" VALUES(9,NULL,1,true)", checkViolation(name, constraint, "9, null, 1, t"))
						wantTuples(t, db, "SELECT id,a,b,flag FROM "+name+" ORDER BY id", tc.rows)
					}
				}
			}
			if tc.name == "null_arm" {
				var dump bytes.Buffer
				if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(context.Background(), db); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				for i, name := range []string{"original", "copy0", "copy1"} {
					constraint := "c"
					if i > 0 {
						constraint = fmt.Sprintf("c%d", i-1)
					}
					wantTuples(t, db, "SELECT id,a,b,flag FROM "+name+" ORDER BY id", tc.rows)
					checkExecError(t, db, true, "UPDATE "+name+" SET a=0,flag=true WHERE id=1", checkViolation(name, constraint, "1, 0, 1, t"))
					wantTuples(t, db, "SELECT id,a,b,flag FROM "+name+" ORDER BY id", tc.rows)
				}
				for mode, expr := range []string{tc.compact, tc.pretty} {
					deparseReviewTypedValue(t, db, fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1), "TEXT", expr)
				}
			}
		})
	}
}

func TestDeparseConstraintsReviewGreatestFloorConsumers(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE original(id INT,a INT,CONSTRAINT c CHECK(GREATEST(a,1)>0))")
	for mode, expr := range []string{"(GREATEST(a, 1) > 0)", "GREATEST(a, 1) > 0"} {
		q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
		deparseReviewTypedValue(t, db, q, "TEXT", "CHECK ("+expr+")")
		var actual string
		if err := db.QueryRow(q).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		execSQL(t, db, fmt.Sprintf("CREATE TABLE copy%d(id INT,a INT,CONSTRAINT copied %s)", mode, actual))
	}
	for _, name := range []string{"original", "copy0", "copy1"} {
		for _, prepared := range []bool{false, true} {
			tableCoreExec(t, db, prepared, "INSERT INTO "+name+" VALUES(1,-100),(2,0),(3,NULL)", 3)
			wantTuples(t, db, "SELECT id,a FROM "+name+" ORDER BY id", [][]any{{int64(1), int64(-100)}, {int64(2), int64(0)}, {int64(3), nil}})
			execSQL(t, db, "DELETE FROM "+name)
		}
	}
}
