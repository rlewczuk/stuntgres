package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Direct private binding tests separate definition capture from parameter
// policy; using noParameters for MVs would change their established diagnostic.
func TestMaterializedViewDefinitionCapture(t *testing.T) {
	for _, query := range []string{
		"SELECT relname, 'marker'::text AS label FROM pg_catalog.pg_class",
		"VALUES (1::int, 'marker'::text), (2::int, NULL::text)",
		"SELECT relkind, count(*) AS n FROM pg_catalog.pg_class WHERE relname <> 'skip' GROUP BY relkind HAVING count(*) > 0 ORDER BY relkind LIMIT 2 OFFSET 0",
		"SELECT relname, row_number() OVER (PARTITION BY relkind ORDER BY relname) AS n FROM pg_catalog.pg_class WHERE relname <> 'skip' ORDER BY relname LIMIT 2",
		"SELECT a.relname FROM pg_catalog.pg_class a JOIN pg_catalog.pg_class n ON a.oid = n.oid WHERE n.relname = 'pg_class'",
		"SELECT 1 AS i UNION ALL SELECT 2 AS i ORDER BY i LIMIT 2",
		"SELECT 'pg_catalog.int4'::regtype AS typ",
	} {
		t.Run(query, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			statements, err := parser.Parse("CREATE MATERIALIZED VIEW public.captured AS " + query + " WITH NO DATA")
			if err != nil {
				t.Fatal(err)
			}
			s := statements[0].(*parser.CreateQueryTable)
			input, _ := sessionPresentation(op.session)
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			b, err := e.describe(context.Background(), s, auth, ids.ObjectRef{}, input, op, nil, nil)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			stage, d, skipped, err := e.prepareQueryTableLocked(op, auth, s, b)
			e.mu.Unlock()
			if err != nil || skipped {
				t.Fatalf("freeze stage: %v skipped=%v", err, skipped)
			}
			if len(viewOf(d).literals) == 0 {
				t.Fatal("missing frozen literal occurrences")
			}
			if len(b.querySources) != 0 && (len(viewOf(d).columns) == 0 || len(viewOf(d).namespaces) == 0) {
				t.Fatal("missing source column/frame analysis")
			}
			if err := (relationProvider{}).Validate(e.stageRead(stage), d); err != nil {
				t.Fatal("captured definition is invalid:", err)
			}
		})
	}
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	statements, err := parser.Parse("CREATE MATERIALIZED VIEW public.parameterized AS SELECT $1::int AS i")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := sessionPresentation(op.session)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	_, err = e.describe(context.Background(), statements[0], auth, ids.ObjectRef{}, input, op, nil, nil)
	e.mu.Unlock()
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "0A000" || diagnostic.Message != "materialized views may not be defined using bound parameters" {
		t.Fatalf("capture changed parameter policy: %v", err)
	}
}
