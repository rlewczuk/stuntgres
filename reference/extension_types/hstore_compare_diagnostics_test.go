package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreComparisonDiagnosticChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	valid := "SELECT $1::hstore=$2::hstore AS eq"
	stmt, err := c.PrepareContext(p.Ctx, valid)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: valid}
	for _, op := range []string{"<", "<=", ">", ">="} {
		q := "SELECT ''::hstore " + op + " ''::hstore"
		want := hstoreComparisonUnsupported(op)
		// No held unsupported statement can exist: the fresh Prepare itself fails.
		hstoreError(t, p, b, c, q, false, true, false, want)
		hstoreError(t, p, b, c, q, true, true, true, want)
		for _, q := range []string{
			"SELECT NULL::hstore " + op + " NULL::hstore",
			"SELECT v " + op + " ''::hstore FROM hs_cmp_diag_empty",
		} {
			want.Position = strconv.Itoa(strings.Index(q, " "+op+" ") + 2)
			hstoreError(t, p, b, c, q, false, true, false, want)
			hstoreError(t, p, b, c, q, true, true, true, want)
		}
		hstoreRun(t, p, b, held, fixture.Select(valid, []string{"eq"}, []string{"BOOL"}, [][]any{{true}}, "b=>2,a=>1", "a=>1,b=>2"))
		hstoreRun(t, p, b, held, fixture.Select(valid, []string{"eq"}, []string{"BOOL"}, [][]any{{nil}}, nil, "a=>1"))
		hstoreRun(t, p, b, held, fixture.Select(valid, []string{"eq"}, []string{"BOOL"}, [][]any{{false}}, "a=>bc", "ab=>c"))
	}
}

func TestReferenceHstoreComparisonDiagnostics(t *testing.T) {
	hstoreComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_cmp_diag_empty(v hstore)", 0))
		hstoreComparisonDiagnosticChecks(t, p, b, c)
		hstoreComparisonUniqueSetup(t, p, b, c)
		hstoreComparisonUniqueChecks(t, p, b, c)
	})
}
