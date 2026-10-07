package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

var betweenForms = []string{"BETWEEN", "NOT BETWEEN", "BETWEEN SYMMETRIC", "NOT BETWEEN SYMMETRIC"}

func betweenSetup(t *testing.T) (*stuntgres.Driver, *sql.DB) {
	t.Helper()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "between")
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v INTEGER)")
	execSQL(t, db, "INSERT INTO t VALUES(1,0),(2,1),(3,2),(4,3),(5,4),(6,NULL)")
	return d, db
}

func TestBetweenMatrix(t *testing.T) {
	_, db := betweenSetup(t)
	for _, tc := range []struct {
		bounds string
		values [][]any
		ids    [][]int64
	}{
		{"1 AND 3", [][]any{{false, true, false, true}, {true, false, true, false}, {true, false, true, false}, {true, false, true, false}, {false, true, false, true}, {nil, nil, nil, nil}}, [][]int64{{2, 3, 4}, {1, 5}, {2, 3, 4}, {1, 5}}},
		{"3 AND 1", [][]any{{false, true, false, true}, {false, true, true, false}, {false, true, true, false}, {false, true, true, false}, {false, true, false, true}, {nil, nil, nil, nil}}, [][]int64{{}, {1, 2, 3, 4, 5}, {2, 3, 4}, {1, 5}}},
		{"2 AND 2", [][]any{{false, true, false, true}, {false, true, false, true}, {true, false, true, false}, {false, true, false, true}, {false, true, false, true}, {nil, nil, nil, nil}}, [][]int64{{3}, {1, 2, 4, 5}, {3}, {1, 2, 4, 5}}},
	} {
		for form, keyword := range betweenForms {
			want := [][]any{}
			for _, row := range tc.values {
				want = append(want, []any{row[form]})
			}
			expr := "v " + keyword + " " + tc.bounds
			arithmeticBounds := map[string]string{"1 AND 3": "(1+0) AND (2+1)", "3 AND 1": "(2+1) AND (1+0)", "2 AND 2": "(1+1) AND (3-1)"}[tc.bounds]
			compareOpsDirectPrepared(t, db, "SELECT v "+keyword+" "+arithmeticBounds+" FROM t ORDER BY id", []string{"BOOL"}, want)
			compareOpsDirectPrepared(t, db, "SELECT "+expr+" FROM t ORDER BY id", []string{"BOOL"}, want)
			want = [][]any{}
			for _, id := range tc.ids[form] {
				want = append(want, []any{id})
			}
			compareOpsDirectPrepared(t, db, "SELECT id FROM t WHERE "+expr+" ORDER BY id", []string{"INT4"}, want)
			compareOpsDirectPrepared(t, db, "SELECT id FROM t WHERE v "+keyword+" "+arithmeticBounds+" ORDER BY id", []string{"INT4"}, want)
		}
	}
	execSQL(t, db, "CREATE TABLE empty(v INTEGER)")
	for _, form := range betweenForms {
		compareOpsDirectPrepared(t, db, "SELECT v "+form+" 1 AND 3 FROM empty", []string{"BOOL"}, [][]any{})
		compareOpsDirectPrepared(t, db, "SELECT 2 "+form+" 1 AND 3 FROM t WHERE id<3", []string{"BOOL"}, [][]any{{form == "BETWEEN" || form == "BETWEEN SYMMETRIC"}, {form == "BETWEEN" || form == "BETWEEN SYMMETRIC"}})
	}
}

func TestBetweenNullPositions(t *testing.T) {
	_, db := betweenSetup(t)
	execSQL(t, db, "CREATE TABLE n(id INTEGER,a INTEGER,l INTEGER,u INTEGER)")
	execSQL(t, db, "INSERT INTO n VALUES(1,NULL,1,3),(2,5,NULL,3),(3,5,3,NULL),(4,1,NULL,3),(5,1,3,NULL),(6,3,NULL,3),(7,3,3,NULL),(8,2,NULL,NULL),(9,NULL,NULL,NULL)")
	for form, keyword := range betweenForms {
		want := [][]any{{nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}}
		ids := [][]any{}
		if form == 0 {
			want[1][0] = false
			want[4][0] = false
		}
		if form == 1 {
			want[1][0] = true
			want[4][0] = true
			ids = [][]any{{int64(2)}, {int64(5)}}
		}
		expr := "a " + keyword + " l AND u"
		compareOpsDirectPrepared(t, db, "SELECT "+expr+" FROM n ORDER BY id", []string{"BOOL"}, want)
		compareOpsDirectPrepared(t, db, "SELECT id FROM n WHERE "+expr+" ORDER BY id", []string{"INT4"}, ids)
		stmt, err := db.Prepare("SELECT $1 " + keyword + " $2::INTEGER AND $3::INTEGER FROM t WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		for row, args := range [][]any{{nil, 1, 3}, {5, nil, 3}, {5, 3, nil}, {1, nil, 3}, {1, 3, nil}, {3, nil, 3}, {3, 3, nil}, {2, nil, nil}, {nil, nil, nil}} {
			rows, err := stmt.Query(args...)
			compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{want[row]})
		}
		stmt.Close()
		compareOpsDirectPrepared(t, db, "SELECT NULL "+keyword+" 1 AND 3 FROM t WHERE id=1", []string{"BOOL"}, [][]any{{nil}})
	}
}

func TestBetweenGrammar(t *testing.T) {
	_, db := betweenSetup(t)
	for _, expr := range []string{"TRUE BETWEEN (1 BETWEEN 0 AND 2) AND TRUE", "TRUE BETWEEN FALSE AND (2 BETWEEN 1 AND 3)", "(TRUE BETWEEN (FALSE OR TRUE) AND TRUE)", "TRUE BETWEEN (1<2) AND TRUE", "TRUE BETWEEN (1<2) AND (2<3)", "TRUE BETWEEN 1<2 AND (2<3)", "2 BETWEEN 1 AND 3 = TRUE", "TRUE = 2 BETWEEN 1 AND 3", "(v IS NULL) BETWEEN FALSE AND TRUE", "(id=1) BETWEEN FALSE AND TRUE", "TRUE BETWEEN (TRUE AND TRUE) AND TRUE", "2 BeTwEeN /* lower */ (1+0) AND (2+1)"} {
		compareOpsDirectPrepared(t, db, "SELECT "+expr+" FROM t WHERE id=1", []string{"BOOL"}, [][]any{{true}})
		compareOpsDirectPrepared(t, db, "SELECT id FROM t WHERE ("+expr+") AND id=1", []string{"INT4"}, [][]any{{int64(1)}})
	}
	for _, tc := range []struct {
		expr string
		ids  [][]any
	}{
		{"NOT v BETWEEN 1 AND 3", [][]any{{int64(1)}, {int64(5)}}},
		{"v NOT BETWEEN 1 AND 3 AND v=0", [][]any{{int64(1)}}},
		{"v BETWEEN 1 AND 3 OR v=4", [][]any{{int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}},
		{"v BETWEEN 1 AND 3 AND v=2", [][]any{{int64(3)}}},
	} {
		compareOpsDirectPrepared(t, db, "SELECT id FROM t WHERE "+tc.expr+" ORDER BY id", []string{"INT4"}, tc.ids)
	}
	compareOpsDirectPrepared(t, db, "SELECT v BETWEEN 1 AND 3 IS TRUE FROM t ORDER BY id", []string{"BOOL"}, [][]any{{false}, {true}, {true}, {true}, {false}, {false}})
	execSQL(t, db, "CREATE TABLE words(between INTEGER,\"symmetric\" INTEGER)")
	execSQL(t, db, "INSERT INTO words VALUES(2,3)")
	compareOpsDirectPrepared(t, db, "SELECT between BETWEEN 1 AND 3,\"between\" BETWEEN 2 AND \"symmetric\" FROM words", []string{"BOOL", "BOOL"}, [][]any{{true, true}})
}
