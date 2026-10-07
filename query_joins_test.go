package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func queryJoinsPublicFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{
		"CREATE TABLE a(l TEXT,k INT,q INT)",
		"CREATE TABLE b(r TEXT,q INT,k INT)",
		"CREATE TABLE c(k INT,c TEXT)",
		"CREATE TABLE empty(r TEXT,q INT,k INT)",
		"CREATE TABLE wide(k BIGINT)",
		"INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)",
		"INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)",
		"INSERT INTO c VALUES(1,'c'),(1,'c')",
		"INSERT INTO wide VALUES(1)",
	} {
		execSQL(t, db, q)
	}
	return db
}

// Literal bags deliberately retain duplicates; join traversal order is not a contract.
func TestQueryJoinsRegression(t *testing.T) {
	db := queryJoinsPublicFixture(t)
	for _, tc := range []struct {
		query string
		names, types []string
		want [][]any
		args []any
	}{
		{"SELECT * FROM a JOIN b USING(k)", []string{"k", "l", "q", "r", "q"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4"}, [][]any{{int64(1), "a", int64(9), "x", int64(9)}, {int64(1), "a", int64(9), "x", int64(9)}, {int64(1), "a", int64(9), "x", int64(9)}, {int64(1), "a", int64(9), "x", int64(9)}}, nil},
		{"SELECT u.*,a.k,b.k FROM a JOIN b USING(q,k) AS u", []string{"q", "k", "k", "k"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(1), int64(1), int64(1)}, {int64(9), int64(1), int64(1), int64(1)}, {int64(9), int64(1), int64(1), int64(1)}, {int64(9), int64(1), int64(1), int64(1)}}, nil},
		{"SELECT * FROM a NATURAL INNER JOIN b", []string{"k", "q", "l", "r"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}}, nil},
		{"SELECT a.k,wide.k,k FROM a JOIN wide USING(k)", []string{"k", "k", "k"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(1), int64(1)}}, nil},
		{"SELECT a.l,b.r FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "x"}, {"a", "x"}, {"a", "x"}}, []any{1}},
		{"SELECT a.l,b.r FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, nil, []any{nil}},
		{"SELECT a.l,b.r FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, nil, []any{2}},
		{"SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k",[]string{"?column?"},[]string{"INT4"},[][]any{{int64(1)},{int64(1)},{int64(1)},{int64(1)}},[]any{1}},
		{"SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k",[]string{"?column?"},[]string{"INT4"},nil,[]any{nil}},
		{"SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k",[]string{"?column?"},[]string{"INT4"},nil,[]any{2}},
		{"SELECT * FROM a CROSS JOIN empty", []string{"l", "k", "q", "r", "q", "k"}, []string{"TEXT", "INT4", "INT4", "TEXT", "INT4", "INT4"}, nil, nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) {
				rows, err := db.Query(tc.query, tc.args...)
				queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, true)
			})
			t.Run("prepared", func(t *testing.T) {
				stmt, err := db.Prepare(tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.Query(tc.args...)
				queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, true)
			})
		})
	}
}
