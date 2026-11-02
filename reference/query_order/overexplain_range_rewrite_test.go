package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangeRewriteCTE(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"range_rewrite_owner", "range_rewrite_actor"})
	overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE ROLE range_rewrite_owner", "CREATE ROLE range_rewrite_actor",
			"GRANT USAGE ON SCHEMA public TO range_rewrite_owner,range_rewrite_actor",
			"CREATE TABLE range_rewrite_src(id integer,v text)",
			"CREATE TABLE range_rewrite_sink(id integer,v text)",
			"ALTER TABLE range_rewrite_src OWNER TO range_rewrite_owner",
			"GRANT INSERT(id,v),SELECT(id,v) ON range_rewrite_src TO range_rewrite_actor",
			"GRANT INSERT(id,v),SELECT(id,v) ON range_rewrite_sink TO range_rewrite_owner",
			"CREATE RULE range_rewrite_rule AS ON INSERT TO range_rewrite_src DO INSTEAD INSERT INTO range_rewrite_sink(id,v) VALUES(NEW.id,NEW.v) RETURNING id,v",
			"LOAD 'pg_overexplain'", "SET ROLE range_rewrite_actor",
		} {
			overexplainExec(t, p, b, c, q)
		}
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		const child = "WITH changed AS (INSERT INTO range_rewrite_src(id,v) VALUES($1,$2) RETURNING id,v) SELECT id,v FROM changed ORDER BY id"
		q := overexplainSQL("JSON", "RANGE_TABLE, VERBOSE, COSTS FALSE", child)
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func(r overexplainResult) {
			entries := overexplainRange(t, b, r, "JSON", true)
			if b.PG {
				found := false
				for _, entry := range entries {
					if entry["Kind"] == "relation" && entry["Relation"] == "public.range_rewrite_sink" {
						if overexplainInteger(t, entry["Permission Info Index"], false) <= 0 {
							t.Fatal("PG rewrite lost actual sink permission reference", entry)
						}
						found = true
					}
				}
				if !found {
					t.Fatal("PG rewritten CTE omitted its actual target", entries)
				}
				found = false
				for _, node := range explainJSONNodes(r.document) {
					if node["Relation Name"] == "range_rewrite_sink" {
						found = true
					}
				}
				if !found {
					t.Fatal("PG plan did not operate on the rule sink", r.document)
				}
				return
			}
			original := overexplainRangeNamed(t, b, entries, "relation", "range_rewrite_src")
			actual := overexplainRangeNamed(t, b, entries, "relation", "range_rewrite_sink")
			overexplainRangeRelation(t, p, c, original, "range_rewrite_src", "JSON")
			overexplainRangeRelation(t, p, c, actual, "range_rewrite_sink", "JSON")
			wants := []overexplainRangeDemand{{"INSERT", []string{"id", "v"}, false}, {"SELECT", []string{"id", "v"}, false}}
			overexplainRangeDemands(t, original, "range_rewrite_actor", wants)
			overexplainRangeDemands(t, actual, "range_rewrite_owner", wants)
			originalScope := overexplainRangeOwnerScope(t, r, original["Entry ID"])
			actualScope := overexplainRangeOwnerScope(t, r, actual["Entry ID"])
			if originalScope["Scope ID"] == actualScope["Scope ID"] || originalScope["Kind"] != "caller" || actualScope["Kind"] != "rule" || originalScope["Parent Scope ID"] != actualScope["Parent Scope ID"] {
				t.Fatal("rewritten CTE collapsed caller and actual command ownership", originalScope, actualScope)
			}
			reader := overexplainRangeNamed(t, b, entries, "cte", "changed")
			if reader["Producer Scope ID"] != actualScope["Parent Scope ID"] {
				t.Fatal("rewritten command lost the actual CTE producer/reader edge", reader, actualScope)
			}
			foundProducer := false
			for _, item := range overexplainRangeList(t, overexplainRangeMap(t, r.document[0]["Embedded Range Table"])["Scopes"]) {
				scope := overexplainRangeMap(t, item)
				if scope["Scope ID"] == reader["Producer Scope ID"] && scope["Kind"] == "cte" {
					foundProducer = true
				}
			}
			if !foundProducer {
				t.Fatal("rewritten caller/rule scopes lack their CTE producer container", r.document)
			}
		}
		for _, arg := range []any{"seven", nil} {
			for _, prepared := range []bool{false, true} {
				check(overexplainQuery(t, p, c, q, "JSON", prepared, int64(7), arg))
			}
			rows, err := held.QueryContext(p.Ctx, int64(7), arg)
			if err != nil {
				t.Fatal(err)
			}
			check(overexplainRows(t, rows, "JSON"))
		}
		denied := overexplainSQL("JSON", "RANGE_TABLE", "INSERT INTO range_rewrite_sink VALUES(9,'denied') RETURNING id,v")
		overexplainError(t, p, b, c, denied, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table range_rewrite_sink"), "execute", true)
		overexplainExec(t, p, b, c, "RESET ROLE")
		for _, table := range []string{"range_rewrite_src", "range_rewrite_sink"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, nil))
		}
		// The rule owner witnesses real routing, typed NULL data and rollback.
		// Low-privilege authority is asserted above through RANGE_TABLE and
		// denied direct sink access, not this supplemental live RETURNING path.
		for _, prepared := range []bool{false, true} {
			overexplainExec(t, p, b, c, "BEGIN")
			overexplainExec(t, p, b, c, "SET LOCAL ROLE range_rewrite_owner")
			s := fixture.Select(child, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}}, int64(7), nil)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			overexplainExec(t, p, b, c, "RESET ROLE")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM range_rewrite_sink ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM range_rewrite_src ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, nil))
			overexplainExec(t, p, b, c, "ROLLBACK")
		}
	})
}

func overexplainRangeOwnerScope(t *testing.T, r overexplainResult, entryID any) map[string]any {
	t.Helper()
	var found map[string]any
	for _, doc := range r.document {
		group, ok := doc["Embedded Range Table"].(map[string]any)
		if !ok {
			continue
		}
		for _, item := range overexplainRangeList(t, group["Scopes"]) {
			scope := overexplainRangeMap(t, item)
			for _, item := range overexplainRangeList(t, scope["Entries"]) {
				if overexplainRangeMap(t, item)["Entry ID"] == entryID {
					if found != nil {
						t.Fatal("range entry belongs to multiple scopes", entryID)
					}
					found = scope
				}
			}
		}
	}
	if found == nil {
		t.Fatal("missing actual entry ownership scope", entryID)
	}
	return found
}
