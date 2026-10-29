package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var pptColumns = []string{"partrelid", "partstrat", "partnatts", "partdefid", "partattrs", "partclass", "partcollation", "partexprs"}
var pptTypes = []string{"OID", "CHAR", "INT2", "OID", "INT2VECTOR", "OIDVECTOR", "OIDVECTOR", "PG_NODE_TREE"}

func pptScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		body(t, p, b)
	})
}

func pptRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
}

func pptBoth(run func(fixture.Step), s fixture.Step) {
	run(s)
	run(fixture.Prepared(s))
}

func pptMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return referenceRangeMetadata(pg, pptColumns, pptTypes, []bool{false, false, false, false, false, false, false, true})
}

func pptEmpty(run func(fixture.Step), b fixture.Backend, query string, args ...any) {
	s := fixture.Select(query, pptColumns, pptTypes, nil, args...)
	s.CheckMetadata = pptMetadata(b.PG)
	pptBoth(run, s)
}

// Portable expression meaning, not PostgreSQL's private nodeToString bytes.
func pptFacts(name, strategy string, count int64, attrs, classes, collations string, expressions any) fixture.Step {
	return fixture.Select(`SELECT partrelid=$1::regclass AS identity,partstrat,partnatts,partdefid=0 AS no_default,partattrs::text,partclass::text,partcollation::text,pg_get_expr(partexprs,partrelid) AS expressions FROM pg_catalog.pg_partitioned_table WHERE partrelid=$1::regclass`,
		[]string{"identity", "partstrat", "partnatts", "no_default", "partattrs", "partclass", "partcollation", "expressions"},
		[]string{"BOOL", "CHAR", "INT2", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT"},
		[][]any{{true, strategy, count, true, attrs, classes, collations, expressions}}, name)
}

func pptDefault(run func(fixture.Step), parent, child string) {
	q := `SELECT partdefid=0 AS expected FROM pg_catalog.pg_partitioned_table WHERE partrelid=$1::regclass`
	args := []any{parent}
	if child != "" {
		q = `SELECT partdefid=$2::regclass AS expected FROM pg_catalog.pg_partitioned_table WHERE partrelid=$1::regclass`
		args = append(args, child)
	}
	pptBoth(run, fixture.Select(q, []string{"expected"}, []string{"BOOL"}, [][]any{{true}}, args...))
}

func pptID(t *testing.T, p *fixture.Pair, e interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, name string) int64 {
	t.Helper()
	var id int64
	if err := e.QueryRowContext(p.Ctx, "SELECT $1::regclass::oid::bigint", name).Scan(&id); err != nil || id <= 0 {
		t.Fatal("relation identity", id, err)
	}
	return id
}
