package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
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
			input, _ := op.Session.Settings.Presentation()
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			b, err := Describe(context.Background(), e.AnalysisHost(op), s, auth, ids.ObjectRef{}, input, nil, nil)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			stage, d, skipped, err := EnginePrepareQueryTableLocked(e, op, auth, s, b)
			e.Mu.Unlock()
			if err != nil || skipped {
				t.Fatalf("freeze stage: %v skipped=%v", err, skipped)
			}
			if len(stored.ViewOf(d).Literals()) == 0 {
				t.Fatal("missing frozen literal occurrences")
			}
			if len(b.QuerySources) != 0 && (len(stored.ViewOf(d).Columns()) == 0 || len(stored.ViewOf(d).Namespaces()) == 0) {
				t.Fatal("missing source column/frame analysis")
			}
			if err := (RelationProvider).Validate(e.StageRead(stage), d); err != nil {
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
	input, _ := op.Session.Settings.Presentation()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	_, err = Describe(context.Background(), e.AnalysisHost(op), statements[0], auth, ids.ObjectRef{}, input, nil, nil)
	e.Mu.Unlock()
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "0A000" || diagnostic.Message != "materialized views may not be defined using bound parameters" {
		t.Fatalf("capture changed parameter policy: %v", err)
	}
}
