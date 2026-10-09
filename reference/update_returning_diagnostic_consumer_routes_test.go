package reference_test

import (
	"strconv"
	"testing"
)

func TestReferenceUpdateReturningDiagnosticsConsumerRoutes(t *testing.T) {
	cases := []struct {
		q string
		d referenceDiagnostic
	}{
		{"UPDATE t SET absent=1 WHERE missing_where=1 RETURNING missing_return", referenceDiagnostic{Code: "42703", Message: `column "missing_where" does not exist`, Position: "29"}},
		{"UPDATE t SET absent=1 RETURNING missing_return", referenceDiagnostic{Code: "42703", Message: `column "missing_return" does not exist`, Position: "33"}},
		{"UPDATE t SET id=CAST('bad' AS int) RETURNING hidden.id", referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "hidden"`, Position: "46"}},
		{"UPDATE t SET id=$1 RETURNING $1", referenceDiagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "17"}},
		{"UPDATE t SET id=1 RETURNING $1,$1::int4", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "29"}},
		{"UPDATE t SET id=1 RETURNING WITH (OLD AS o,OLD AS p) id", referenceDiagnostic{Code: "42601", Message: "OLD cannot be specified multiple times", Position: "44"}},
		{"UPDATE t SET id=1 RETURNING WITH (NEW AS t) id", referenceDiagnostic{Code: "42712", Message: `table name "t" specified more than once`, Position: "35"}},
		{"UPDATE t SET id=1 RETURNING WITH (OLD AS o) old.id", referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "45"}},
		{"UPDATE t SET id=1 RETURNING sum(id)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}},
	}
	for _, populated := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			db := backend.db
			updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,v TEXT)")
			if populated {
				updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(2,'unchanged')")
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
							result, e := db.Exec(prefix+tc.q, 2)
							err = e
							if result != nil {
								t.Fatal("failed Exec exposed result", result)
							}
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
						updateReturningReferenceDiagnostic(t, err, d)
					}
				}
			}
			want := [][]any{}
			if populated {
				want = [][]any{{int64(2), "unchanged"}}
			}
			rows, err := db.Query("SELECT id,v FROM t")
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "v"}, types: []string{"INT4", "TEXT"}, rows: want, bag: false})
		}
	}
}

func TestReferenceUpdateReturningNamespaceErrorsConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1)")
		result, err := db.Exec("UPDATE t SET id=2 RETURNING id AS label,label")
		if result != nil {
			t.Fatal("failed Exec exposed result", result)
		}
		updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "42703", Message: `column "label" does not exist`, Position: "41"})
		updateReturningReferenceExec(t, db, "ALTER TABLE t DROP COLUMN id")
		held, err := db.Prepare("UPDATE t SET absent=1 RETURNING *")
		if held != nil {
			held.Close()
			t.Fatal("failed Prepare exposed statement")
		}
		updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "42601", Message: "RETURNING must have at least one column", Position: "33"})
	}
}

func TestReferenceUpdateReturningParameterPhaseConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(a INT,f BOOL)")
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,TRUE)")
		for _, tc := range []struct {
			q string
			d referenceDiagnostic
		}{
			{"UPDATE t SET a=$1,f=$1 RETURNING 1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "21"}},
			{"UPDATE t SET a=$1,f=$1 RETURNING $1::int4", referenceDiagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression.", Position: "21"}},
		} {
			result, err := db.Exec(tc.q, 2)
			if result != nil {
				t.Fatal("failed Exec exposed result", result)
			}
			updateReturningReferenceDiagnostic(t, err, tc.d)
			held, err := db.Prepare(tc.q)
			if held != nil {
				held.Close()
				t.Fatal("invalid handle")
			}
			updateReturningReferenceDiagnostic(t, err, tc.d)
		}
		rows, err := db.Query("SELECT a,f FROM t")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"a", "f"}, types: []string{"INT4", "BOOL"}, rows: [][]any{{int64(1), true}}, bag: false})
	}
}
