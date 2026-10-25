package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func defaultAttributeCalls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	parametersValue(t, p, b, c, "SELECT "+name+"() AS v", "v", "INT4", nil)
	parametersValue(t, p, b, c, "SELECT "+name+"(17) AS v", "v", "INT4", int64(17))
	parametersValue(t, p, b, c, "SELECT "+name+"(NULL) AS v", "v", "INT4", nil)
	parametersHeld(t, p, b, c, "SELECT "+name+"($1) AS v", "INT4", [][]any{{17}, {23}, {nil}}, []any{int64(17), int64(23), nil})
	parametersHeld(t, p, b, c, "SELECT "+name+"() AS v", "INT4", [][]any{nil, nil}, []any{nil, nil})
}

// Every component of the full edge address is checked relationally, without
// pinning allocator OIDs. The WHERE admits every edge to this relation, including
// unexpected whole-relation and other-field captures.
func defaultAttributeEdges(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, signature, relation string, attrs []int64, counts []int64) {
	t.Helper()
	q := "SELECT d.classid='pg_proc'::regclass AS procedure_class,d.objid='" + signature + "'::regprocedure AS routine_identity,d.objsubid,d.refclassid='pg_class'::regclass AS relation_class,d.refobjid='" + relation + "'::regclass AS relation_identity,d.refobjsubid,d.deptype::text AS kind FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid='" + signature + "'::regprocedure AND d.refclassid='pg_class'::regclass AND d.refobjid='" + relation + "'::regclass GROUP BY d.classid,d.objid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype ORDER BY d.refobjsubid,d.deptype,d.objsubid"
	want := make([][]any, len(attrs))
	for i, a := range attrs {
		want[i] = []any{true, true, int64(0), true, true, a, "n"}
	}
	s := fixture.Select(q, []string{"procedure_class", "routine_identity", "objsubid", "relation_class", "relation_identity", "refobjsubid", "kind"}, []string{"BOOL", "BOOL", "INT4", "BOOL", "BOOL", "INT4", "TEXT"}, want)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	raw := "SELECT d.refobjsubid,count(*) AS n FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid='" + signature + "'::regprocedure AND d.refclassid='pg_class'::regclass AND d.refobjid='" + relation + "'::regclass GROUP BY d.refobjsubid ORDER BY d.refobjsubid"
	physical := make([][]any, len(attrs))
	for i, a := range attrs {
		physical[i] = []any{a, counts[i]}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(raw, []string{"refobjsubid", "n"}, []string{"INT4", "INT8"}, physical))
}

func defaultAttributeTypeEdge(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, signature, typ string, count int64) {
	t.Helper()
	q := "SELECT d.objsubid,d.refobjsubid,d.deptype::text AS kind FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid='" + signature + "'::regprocedure AND d.refclassid='pg_type'::regclass AND d.refobjid='" + typ + "'::regtype GROUP BY d.objsubid,d.refobjsubid,d.deptype ORDER BY d.objsubid,d.refobjsubid,d.deptype"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{"objsubid", "refobjsubid", "kind"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(0), int64(0), "n"}}))
	raw := "SELECT count(*) AS n FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid='" + signature + "'::regprocedure AND d.refclassid='pg_type'::regclass AND d.refobjid='" + typ + "'::regtype"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(raw, []string{"n"}, []string{"INT8"}, [][]any{{count}}))
}

func defaultAttributeLabels(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, signature, schema, relation, attribute string) {
	t.Helper()
	var declaration string
	if b.PG {
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_functiondef('"+signature+"'::regprocedure)").Scan(&declaration); err != nil {
			t.Fatal(err)
		}
	} else {
		// Native controls expose rendered declarations through Dump, not a
		// SQL deparse builtin. Inspect the named routine header itself so
		// current labels elsewhere in the dump cannot satisfy this assertion.
		var output bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &output); err != nil {
			t.Fatal(err)
		}
		name, _, _ := strings.Cut(signature, "(")
		prefix := "CREATE FUNCTION \"" + strings.ReplaceAll(name, ".", "\".\"") + "\"("
		start := strings.Index(output.String(), prefix)
		if start < 0 {
			t.Fatalf("missing rendered routine %s in dump: %s", signature, output.String())
		}
		declaration, _, _ = strings.Cut(output.String()[start:], "\n")
	}
	declaration = strings.ReplaceAll(declaration, "\"", "")
	if !strings.Contains(declaration, "DEFAULT ") {
		t.Fatalf("rendered routine has no declaration default: %q", declaration)
	}
	for _, label := range []string{schema + "." + relation, attribute} {
		if !strings.Contains(declaration, label) {
			t.Errorf("current captured label %q missing from declaration=%q", label, declaration)
		}
	}
}

// Stored columns and aggregate counts have known native nullability; pq reports
// unknown nullability. Callable computed values continue using parametersValue.
func defaultAttributeStoredValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, typ string, value any, nullable bool) {
	t.Helper()
	s := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}})
	s.CheckMetadata = parametersMetadataNullability(b, "v", typ, nullable && !b.PG, !b.PG)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}
