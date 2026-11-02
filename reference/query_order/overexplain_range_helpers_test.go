package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func overexplainRangeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			db, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.DB, b.Recorder = db, recorder
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			defer func() {
				if t.Failed() {
					c.ExecContext(p.Ctx, "ROLLBACK")
				}
			}()
			body(t, p, b, c)
			if len(notices) != 0 {
				t.Errorf("unexpected notices: %#v", notices)
			}
		})
	}
}

func overexplainRangeUnloaded(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
		for _, option := range []string{"RANGE_TABLE", "RANGE_TABLE FALSE"} {
			q := "/*é*/ " + overexplainSQL(format, option, "SELECT 1")
			overexplainError(t, p, b, c, q, overexplainDiagnostic(q, "42601", `unrecognized EXPLAIN option "range_table"`, "RANGE_TABLE"), "execute", true)
		}
	}
}

func overexplainRangeSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	overexplainExec(t, p, b, c, "CREATE TABLE range_src(id integer,v text)")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO range_src VALUES(1,'one'),(2,NULL),(3,'three')", 3))
	overexplainExec(t, p, b, c, "CREATE SEQUENCE range_seq START 1")
}

func overexplainRangeValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	s := fixture.Select("SELECT id,v FROM range_src ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func overexplainRangeMap(t *testing.T, value any) map[string]any {
	t.Helper()
	m, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected mapping, got %T: %v", value, value)
	}
	return m
}

func overexplainRangeList(t *testing.T, value any) []any {
	t.Helper()
	list, ok := value.([]any)
	if !ok {
		t.Fatalf("expected explicit list, got %T: %v", value, value)
	}
	return list
}

func overexplainRangeStrings(t *testing.T, value any) []string {
	t.Helper()
	list := overexplainRangeList(t, value)
	out := make([]string, len(list))
	for i, v := range list {
		var ok bool
		out[i], ok = v.(string)
		if !ok {
			t.Fatalf("nonstring list item: %T %v", v, v)
		}
	}
	return out
}

func overexplainRangeBoolean(t *testing.T, value any, xml bool) bool {
	t.Helper()
	if xml {
		if value != "true" && value != "false" {
			t.Fatalf("invalid XML Boolean %v", value)
		}
		return value == "true"
	}
	v, ok := value.(bool)
	if !ok {
		t.Fatalf("non-Boolean %T %v", value, value)
	}
	return v
}

// Validate each backend's actual schema, not a normalized common RTE model.
// The flattened return is only a search aid; native ownership remains in Scopes.
func overexplainRange(t *testing.T, b fixture.Backend, r overexplainResult, format string, enabled bool) []map[string]any {
	t.Helper()
	if format == "TEXT" {
		if !enabled {
			for _, marker := range []string{"Embedded Range Table:", "RTI ", "Scan RTI:", "Scan RTIs:", "Nominal RTI:", "Exclude Relation RTI:", "Append RTIs:", "Unprunable RTIs:", "Result RTIs:", "Required Permissions:"} {
				if strings.Contains(r.text, marker) {
					t.Fatalf("disabled RANGE_TABLE leaked %q: %s", marker, r.text)
				}
			}
		} else if !b.PG {
			explainContains(t, []string{r.text}, "Embedded Range Table:", "Scope ID:")
		}
		return nil
	}
	var result []map[string]any
	for i, doc := range r.document {
		key, other := "Embedded Range Table", "Range Table"
		if b.PG {
			key, other = other, key
		}
		if _, exists := doc[other]; exists {
			t.Fatalf("backend emitted foreign schema %q: %v", other, doc)
		}
		value, exists := doc[key]
		wantRange := enabled
		if !b.PG && i != 0 {
			switch overexplainRangeMap(t, doc["Plan"])["Node Type"] {
			case "CTE", "SubPlan":
				wantRange = false // The renderer's primary query owns the shared group.
			}
		}
		if exists != wantRange {
			t.Fatalf("%s presence=%t want %t: %v", key, exists, wantRange, doc)
		}
		if !wantRange {
			for _, field := range []string{"Unprunable RTIs", "Result RTIs"} {
				if _, exists := doc[field]; exists {
					t.Errorf("disabled RANGE_TABLE leaked query property %s", field)
				}
			}
			explainJSONVisit(overexplainRangeMap(t, doc["Plan"]), func(n map[string]any) {
				for _, field := range []string{"Scan RTI", "Scan RTIs", "Nominal RTI", "Exclude Relation RTI", "Append RTIs", "Embedded Range Table"} {
					if _, exists := n[field]; exists {
						t.Errorf("disabled RANGE_TABLE leaked %s", field)
					}
				}
			})
			continue
		}
		if b.PG {
			result = append(result, overexplainRangePG(t, doc, value, format)...)
		} else {
			result = append(result, overexplainRangeNative(t, value, format)...)
			explainJSONVisit(overexplainRangeMap(t, doc["Plan"]), func(n map[string]any) {
				for _, field := range []string{"Scan RTI", "Scan RTIs", "Nominal RTI", "Exclude Relation RTI", "Append RTIs", "Embedded Range Table"} {
					if _, exists := n[field]; exists {
						t.Errorf("native plan invented PG RTI or nested query group %s", field)
					}
				}
			})
		}
	}
	return result
}

func overexplainRangePG(t *testing.T, doc map[string]any, value any, format string) []map[string]any {
	t.Helper()
	var entries []map[string]any
	ids := map[int64]bool{}
	for _, item := range overexplainRangeList(t, value) {
		e := overexplainRangeMap(t, item)
		id := overexplainInteger(t, e["RTI"], format == "XML")
		if id <= 0 || ids[id] {
			t.Fatal("invalid/duplicate actual PG RTI", e)
		}
		ids[id] = true
		if kind, ok := e["Kind"].(string); !ok || kind == "" {
			t.Fatal("missing PG range kind", e)
		}
		if _, ok := e["Eref"].(string); !ok {
			t.Fatal("PG effective alias is not text", e)
		}
		for _, field := range []string{"Inherited", "In From Clause", "Security Barrier", "Lateral"} {
			overexplainRangeBoolean(t, e[field], format == "XML")
		}
		if index, ok := e["Permission Info Index"]; ok && overexplainInteger(t, index, format == "XML") <= 0 {
			t.Fatal("invalid PG permission-info reference", e)
		}
		for _, field := range []string{"Required Permissions", "Scope ID", "Relation OID"} {
			if _, exists := e[field]; exists {
				t.Fatal("invented embedded property in PG entry", e)
			}
		}
		entries = append(entries, e)
	}
	for _, field := range []string{"Unprunable RTIs", "Result RTIs"} {
		text, ok := doc[field].(string)
		if !ok || text == "" {
			t.Fatalf("PG query-level %s must be string: %v", field, doc)
		}
		if text != "none" {
			for _, token := range strings.Fields(text) {
				var id int64
				if _, err := fmt.Sscan(token, &id); err != nil || !ids[id] {
					t.Fatalf("dangling PG %s=%q", field, text)
				}
			}
		}
	}
	explainJSONVisit(overexplainRangeMap(t, doc["Plan"]), func(n map[string]any) {
		for _, field := range []string{"Scan RTI", "Nominal RTI", "Exclude Relation RTI"} {
			if v, exists := n[field]; exists {
				id := overexplainInteger(t, v, format == "XML")
				if (id == 0 && field != "Scan RTI") || ids[id] {
					continue
				}
				t.Fatalf("dangling %s=%d: %v", field, id, n)
			}
		}
	})
	return entries
}

func overexplainRangeNative(t *testing.T, value any, format string) []map[string]any {
	t.Helper()
	group := overexplainRangeMap(t, value)
	scopes := overexplainRangeList(t, group["Scopes"])
	if len(scopes) == 0 {
		t.Fatal("missing root analysis scope", group)
	}
	scopeIDs, entryIDs := map[int64]bool{}, map[int64]bool{}
	var entries []map[string]any
	for i, item := range scopes {
		s := overexplainRangeMap(t, item)
		id := overexplainInteger(t, s["Scope ID"], format == "XML")
		if id <= 0 || scopeIDs[id] || (i == 0 && id != 1) {
			t.Fatal("invalid statement-local scope", s)
		}
		scopeIDs[id] = true
		if kind, ok := s["Kind"].(string); !ok || kind == "" {
			t.Fatal("missing ownership kind", s)
		}
		if _, hasParent := s["Parent Scope ID"]; hasParent == (i == 0) {
			t.Fatal("root/nonroot parent boundary", s)
		}
		for _, item := range overexplainRangeList(t, s["Entries"]) {
			e := overexplainRangeMap(t, item)
			id := overexplainInteger(t, e["Entry ID"], format == "XML")
			if id <= 0 || entryIDs[id] {
				t.Fatal("invalid range owner identity", e)
			}
			entryIDs[id] = true
			switch e["Kind"] {
			case "relation", "join", "subquery", "cte", "worktable", "values", "function", "named relation":
			default:
				t.Fatal("not an actual range kind", e)
			}
			overexplainRangeStrings(t, e["Columns"])
			if e["Kind"] == "relation" {
				if overexplainInteger(t, e["Relation OID"], format == "XML") <= 0 {
					t.Fatal("missing actual relation identity", e)
				}
				for _, field := range []string{"Schema", "Relation Name", "Relation Kind", "Role"} {
					if v, ok := e[field].(string); !ok || v == "" {
						t.Fatalf("missing %s: %v", field, e)
					}
				}
				if occurrence, exists := e["Occurrence"]; exists && overexplainInteger(t, occurrence, format == "XML") < 0 {
					t.Fatal("fabricated occurrence", e)
				}
				for _, item := range overexplainRangeList(t, e["Required Permissions"]) {
					d := overexplainRangeMap(t, item)
					if principal, ok := d["Principal"].(string); !ok || principal == "" {
						t.Fatal("missing checked principal", d)
					}
					switch d["Privilege"] {
					case "SELECT", "INSERT", "UPDATE", "DELETE":
					default:
						t.Fatal("not an actual SQL privilege requirement", d)
					}
					overexplainRangeStrings(t, d["Columns"])
					overexplainRangeBoolean(t, d["Whole Relation"], format == "XML")
				}
			} else if _, exists := e["Relation OID"]; exists {
				t.Fatal("nonphysical range has invented OID", e)
			}
			for _, field := range []string{"RTI", "Permission Info Index"} {
				if _, exists := e[field]; exists {
					t.Fatal("native fabricated PG internals", e)
				}
			}
			entries = append(entries, e)
		}
	}
	for _, item := range scopes {
		s := overexplainRangeMap(t, item)
		for _, field := range []string{"Parent Scope ID"} {
			if v, exists := s[field]; exists && !scopeIDs[overexplainInteger(t, v, format == "XML")] {
				t.Fatal("dangling scope parent", s)
			}
		}
		if v, exists := s["Owner Entry ID"]; exists && !entryIDs[overexplainInteger(t, v, format == "XML")] {
			t.Fatal("dangling scope owner", s)
		}
		if value, exists := s["Outer Scope IDs"]; exists {
			for _, item := range overexplainRangeList(t, value) {
				if !scopeIDs[overexplainInteger(t, item, format == "XML")] {
					t.Fatal("dangling correlated outer scope", s)
				}
			}
		}
	}
	for _, e := range entries {
		for _, field := range []string{"Child Scope ID", "Producer Scope ID"} {
			if v, exists := e[field]; exists && !scopeIDs[overexplainInteger(t, v, format == "XML")] {
				t.Fatal("dangling child/producer scope", e)
			}
		}
		for _, field := range []string{"Left Entry ID", "Right Entry ID"} {
			if v, exists := e[field]; exists && !entryIDs[overexplainInteger(t, v, format == "XML")] {
				t.Fatal("dangling join input", e)
			}
		}
	}
	return entries
}

func overexplainRangeNamed(t *testing.T, b fixture.Backend, entries []map[string]any, kind, name string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, e := range entries {
		if e["Kind"] != kind {
			continue
		}
		if !b.PG && e["Range Name"] == name {
			found = append(found, e)
		} else if b.PG {
			label, _ := e["Eref"].(string)
			quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
			if strings.HasPrefix(label, name+" (") || strings.HasPrefix(label, quoted+" (") {
				found = append(found, e)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %s range %q; got %d: %v", kind, name, len(found), entries)
	}
	return found[0]
}

func overexplainRangePermission(t *testing.T, e map[string]any, principal, privilege string, columns []string, whole bool) {
	t.Helper()
	for _, item := range overexplainRangeList(t, e["Required Permissions"]) {
		d := overexplainRangeMap(t, item)
		if d["Principal"] == principal && d["Privilege"] == privilege && reflect.DeepEqual(overexplainRangeStrings(t, d["Columns"]), columns) && d["Whole Relation"] == whole {
			return
		}
	}
	t.Fatalf("missing %s %s columns=%v whole=%t: %v", principal, privilege, columns, whole, e)
}

type overexplainRangeDemand struct {
	privilege string
	columns   []string
	whole     bool
}

func overexplainRangeDemands(t *testing.T, e map[string]any, principal string, wants []overexplainRangeDemand) {
	t.Helper()
	key := func(principal, privilege string, columns []string, whole bool) string {
		return fmt.Sprintf("%q/%q/%q/%t", principal, privilege, columns, whole)
	}
	want, got := map[string]int{}, map[string]int{}
	for _, d := range wants {
		want[key(principal, d.privilege, d.columns, d.whole)]++
	}
	for _, item := range overexplainRangeList(t, e["Required Permissions"]) {
		d := overexplainRangeMap(t, item)
		got[key(d["Principal"].(string), d["Privilege"].(string), overexplainRangeStrings(t, d["Columns"]), overexplainRangeBoolean(t, d["Whole Relation"], false))]++
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actual demand multiset=%v want %v: %v", got, want, e)
	}
}

func overexplainRangeRelation(t *testing.T, p *fixture.Pair, c *sql.Conn, e map[string]any, name, format string) {
	t.Helper()
	var oid int64
	var schema, relation, kind string
	if err := c.QueryRowContext(p.Ctx, "SELECT c.oid,n.nspname,c.relname,c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=$1::regclass", name).Scan(&oid, &schema, &relation, &kind); err != nil {
		t.Fatal(err)
	}
	if overexplainInteger(t, e["Relation OID"], format == "XML") != oid || e["Schema"] != schema || e["Relation Name"] != relation || e["Relation Kind"] != kind {
		t.Fatalf("range entry does not identify current checked relation: %v, catalog=%d %s.%s %s", e, oid, schema, relation, kind)
	}
}
