package stuntgres_test

import (
	"strconv"
	"stuntgres"
	"testing"
)

func TestUpdateReturningDiagnostics(t *testing.T) {
	cases := []struct {
		q string
		d stuntgres.Error
	}{
		{"UPDATE t SET absent=1 WHERE missing_where=1 RETURNING missing_return", stuntgres.Error{Code: "42703", Message: `column "missing_where" does not exist`, Position: "29"}},
		{"UPDATE t SET absent=1 RETURNING missing_return", stuntgres.Error{Code: "42703", Message: `column "missing_return" does not exist`, Position: "33"}},
		{"UPDATE t SET id=CAST('bad' AS int) RETURNING hidden.id", stuntgres.Error{Code: "42P01", Message: `missing FROM-clause entry for table "hidden"`, Position: "46"}},
		{"UPDATE t SET id=$1 RETURNING $1", stuntgres.Error{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "17"}},
		{"UPDATE t SET id=1 RETURNING $1,$1::int4", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "29"}},
		{"UPDATE t SET id=1 RETURNING WITH (OLD AS o,OLD AS p) id", stuntgres.Error{Code: "42601", Message: "OLD cannot be specified multiple times", Position: "44"}},
		{"UPDATE t SET id=1 RETURNING WITH (NEW AS t) id", stuntgres.Error{Code: "42712", Message: `table name "t" specified more than once`, Position: "35"}},
		{"UPDATE t SET id=1 RETURNING WITH (OLD AS o) old.id", stuntgres.Error{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "45"}},
		{"UPDATE t SET id=1 RETURNING sum(id)", stuntgres.Error{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}},
	}
	for _, populated := range []bool{false, true} {
		db := openLocal(t, stuntgres.NewDriver(), t.Name())
		execSQL(t, db, "CREATE TABLE t(id INT,v TEXT)")
		if populated {
			execSQL(t, db, "INSERT INTO t VALUES(2,'unchanged')")
		}
		for _, tc := range cases {
			for _, prefix := range []string{"", "/*é*/ "} {
				d := tc.d
				if prefix != "" {
					n, _ := strconv.Atoi(d.Position)
					d.Position = strconv.Itoa(n + 6)
				}
				for _, lane := range []string{"exec", "query", "prepare"} {
					var err error
					switch lane {
					case "exec":
						_, err = db.Exec(prefix+tc.q, 2)
					case "query":
						rows, e := db.Query(prefix+tc.q, 2)
						err = e
						if rows != nil {
							rows.Close()
							t.Fatal("invalid output cursor", tc.q)
						}
					case "prepare":
						stmt, e := db.Prepare(prefix + tc.q)
						err = e
						if stmt != nil {
							stmt.Close()
							t.Fatal("invalid output handle", tc.q)
						}
					}
					typeCoreDiagnostic(t, err, d)
				}
			}
		}
		want := [][]any{}
		if populated {
			want = [][]any{{int64(2), "unchanged"}}
		}
		rows, err := db.Query("SELECT id,v FROM t")
		queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, want, false)
		db.Close()
	}
}
