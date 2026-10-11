package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningDiagnosticsConsumerRoutes(t *testing.T) {
	cases := []struct {
		q string
		d fixture.Diagnostic
	}{
		{"UPDATE t SET absent=1 WHERE missing_where=1 RETURNING missing_return", fixture.Diagnostic{Code: "42703", Message: `column "missing_where" does not exist`, Position: "29"}},
		{"UPDATE t SET absent=1 RETURNING missing_return", fixture.Diagnostic{Code: "42703", Message: `column "missing_return" does not exist`, Position: "33"}},
		{"UPDATE t SET id=CAST('bad' AS int) RETURNING hidden.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "hidden"`, Position: "46"}},
		{"UPDATE t SET id=$1 RETURNING $1", fixture.Diagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "17"}},
		{"UPDATE t SET id=1 RETURNING $1,$1::int4", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "29"}},
		{"UPDATE t SET id=1 RETURNING WITH (OLD AS o,OLD AS p) id", fixture.Diagnostic{Code: "42601", Message: "OLD cannot be specified multiple times", Position: "44"}},
		{"UPDATE t SET id=1 RETURNING WITH (NEW AS t) id", fixture.Diagnostic{Code: "42712", Message: `table name "t" specified more than once`, Position: "35"}},
		{"UPDATE t SET id=1 RETURNING WITH (OLD AS o) old.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "45"}},
		{"UPDATE t SET id=1 RETURNING sum(id)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}},
	}
	for _, populated := range []bool{false, true} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			db := backend.DB
			fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,v TEXT)")
			if populated {
				fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(2,'unchanged')")
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
						fixture.UpdateReturningReferenceDiagnostic(t, err, d)
					}
				}
			}
			want := [][]any{}
			if populated {
				want = [][]any{{int64(2), "unchanged"}}
			}
			rows, err := db.Query("SELECT id,v FROM t")
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "v"}, Types: []string{"INT4", "TEXT"}, Rows: want, Bag: false})
		}
	}
}

func TestReferenceUpdateReturningNamespaceErrorsConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1)")
		result, err := db.Exec("UPDATE t SET id=2 RETURNING id AS label,label")
		if result != nil {
			t.Fatal("failed Exec exposed result", result)
		}
		fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "42703", Message: `column "label" does not exist`, Position: "41"})
		fixture.UpdateReturningReferenceExec(t, db, "ALTER TABLE t DROP COLUMN id")
		held, err := db.Prepare("UPDATE t SET absent=1 RETURNING *")
		if held != nil {
			held.Close()
			t.Fatal("failed Prepare exposed statement")
		}
		fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "42601", Message: "RETURNING must have at least one column", Position: "33"})
	}
}

func TestReferenceUpdateReturningParameterPhaseConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(a INT,f BOOL)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,TRUE)")
		for _, tc := range []struct {
			q string
			d fixture.Diagnostic
		}{
			{"UPDATE t SET a=$1,f=$1 RETURNING 1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "21"}},
			{"UPDATE t SET a=$1,f=$1 RETURNING $1::int4", fixture.Diagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression.", Position: "21"}},
		} {
			result, err := db.Exec(tc.q, 2)
			if result != nil {
				t.Fatal("failed Exec exposed result", result)
			}
			fixture.UpdateReturningReferenceDiagnostic(t, err, tc.d)
			held, err := db.Prepare(tc.q)
			if held != nil {
				held.Close()
				t.Fatal("invalid handle")
			}
			fixture.UpdateReturningReferenceDiagnostic(t, err, tc.d)
		}
		rows, err := db.Query("SELECT a,f FROM t")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"a", "f"}, Types: []string{"INT4", "BOOL"}, Rows: [][]any{{int64(1), true}}, Bag: false})
	}
}
