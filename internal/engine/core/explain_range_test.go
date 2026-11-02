package core_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/explain"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// Bind and render within the genuine admitted statement's lifetime. Tests
// compare detached facts with that same accepted graph, not a rebound stand-in.
func explainRangeOwner(t *testing.T, e *core.Engine, s *session.Session, query string, enabled bool, check func(analyze.Binding, core.AuthorizationView, []map[string]any), before ...func(analyze.Binding)) {
	t.Helper()
	statement := enginetest.Statement(t, query)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if !scope.Finished {
			scope.Finish(context.Canceled)
		}
	}()
	description, err := scope.Describe(statement)
	if err != nil {
		t.Fatal(err)
	}
	defer e.RetirePreparedDescription(&description)
	e.Mu.Lock()
	if err := scope.RefreshRelationGrantsLocked(e); err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	auth, err := e.StatementAuthorizationLocked(op, statement)
	if err == nil {
		err = scope.CaptureIdentityLocked(auth)
	}
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	binding, err := core.Describe(op.Ctx, e.AnalysisHost(op), statement, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	if err := view.NormalizeRuleCTEsLocked(&e.Catalog.Owner, auth, statement, &binding); err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	if len(before) != 0 {
		before[0](binding)
	}
	host := core.HostOf(scope).(explain.Host)
	options := explain.Options{RangeTable: enabled, Format: "json"}
	renderer, err := explain.NewRenderer(host, auth, options)
	if err == nil {
		_, err = renderer.Command(statement, explain.Bound{Binding: binding})
	}
	typ, _ := auth.Catalog.LookupType(sqltype.JSON)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := explain.Output(op.Ctx, []catalog.Column{{Name: "QUERY PLAN", Usage: typ.Usage()}}, options, []*explain.Renderer{renderer}, nil, explain.Summaries{})
	if err != nil {
		t.Fatal(err)
	}
	text, err := rows[0][0].Type().Output(op.Ctx, rows[0][0])
	if err != nil {
		t.Fatal(err)
	}
	var document []map[string]any
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		t.Fatal(err)
	}
	check(binding, auth, document)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
}

func explainRangeEntries(t *testing.T, document []map[string]any) []map[string]any {
	t.Helper()
	group := document[0]["Embedded Range Table"].(map[string]any)
	var entries []map[string]any
	for _, item := range group["Scopes"].([]any) {
		for _, item := range item.(map[string]any)["Entries"].([]any) {
			entries = append(entries, item.(map[string]any))
		}
	}
	for _, auxiliary := range document[1:] {
		if _, exists := auxiliary["Embedded Range Table"]; exists {
			t.Fatal("auxiliary duplicated command range group")
		}
	}
	return entries
}

func TestExplainRangeBindings(t *testing.T) {
	t.Run("invocation-named-relation", explainRangeNamedOwner)
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{"CREATE TABLE range_owner(id integer,v text)", "CREATE VIEW range_owner_view AS SELECT id,v FROM range_owner"} {
		enginetest.SessionSQL(t, s, query)
	}
	for _, query := range []string{
		"SELECT a.id,b.v FROM range_owner a JOIN range_owner b ON a.id=b.id",
		"SELECT j.a_id,j.b_v FROM (range_owner a JOIN range_owner b ON a.id=b.id) j(a_id,a_v,b_id,b_v)",
		"SELECT d.id,(SELECT b.v FROM range_owner b WHERE b.id=d.id) FROM (SELECT a.id FROM range_owner a OFFSET 0) d",
		"WITH x AS MATERIALIZED (SELECT id FROM range_owner) SELECT l.id,r.id FROM x l CROSS JOIN x r",
		"WITH x AS MATERIALIZED (SELECT id FROM range_owner) SELECT (SELECT id FROM x LIMIT 1)",
		"WITH unused AS (SELECT v FROM range_owner), x AS MATERIALIZED (SELECT id FROM range_owner) SELECT id FROM x",
		"SELECT EXISTS(SELECT DISTINCT id FROM range_owner ORDER BY id LIMIT 1)",
		"SELECT EXISTS(SELECT row_number() OVER(ORDER BY (SELECT id FROM range_owner LIMIT 1)) FROM range_owner)",
		"INSERT INTO range_owner AS t(id,v) SELECT t.id,t.v FROM range_owner t RETURNING t.id",
		"SELECT l.id,r.v FROM range_owner_view l JOIN range_owner_view r ON l.id=r.id",
	} {
		t.Run(query, func(t *testing.T) {
			explainRangeOwner(t, e, s, query, true, func(binding analyze.Binding, _ core.AuthorizationView, document []map[string]any) {
				entries := explainRangeEntries(t, document)
				for occurrence, leaf := range binding.QuerySources {
					found := false
					for _, entry := range entries {
						if entry["Kind"] == "relation" && entry["Occurrence"] == float64(occurrence) && entry["Relation OID"] == float64(leaf.Ref.Address.ObjectID) {
							found = true
						}
					}
					if !found {
						t.Fatal("accepted source manifest disappeared behind inactive presentation", occurrence, leaf.AnalysisOnly, entries)
					}
				}
				actual := map[plan.Node]bool{}
				plan.VisitCTEQuery(binding.Query.Root, func(node plan.Node) { actual[node] = true }, func(expr.Expr) {}, true)
				for node := range actual {
					switch n := node.(type) {
					case *plan.SourceNode:
						var matches []map[string]any
						for _, entry := range entries {
							if entry["Kind"] == "relation" && entry["Occurrence"] == float64(n.Occurrence) && entry["Range Name"] == n.RangeName && entry["Relation OID"] == float64(n.Ref.Address.ObjectID) {
								matches = append(matches, entry)
							}
						}
						if len(matches) == 0 {
							t.Fatal("accepted source disappeared from range projection", n, entries)
						}
						for _, entry := range matches {
							want := make([]any, len(n.Schema()))
							for i, column := range n.Schema() {
								want[i] = column.Name
								if i < len(n.ColumnAliases) {
									want[i] = n.ColumnAliases[i]
								}
							}
							if !reflect.DeepEqual(entry["Columns"], want) {
								t.Fatal("range labels not backed by accepted source namespace", entry, want)
							}
						}
					case *plan.JoinNode:
						if n.RangeName != "j" {
							continue
						}
						if !reflect.DeepEqual(n.ColumnAliases, []string{"a_id", "a_v", "b_id", "b_v"}) || n.Schema()[0].Name != "id" {
							t.Fatal("alias changed binding schema or was lost", n)
						}
						copy, err := plan.RewriteQueryExpressions(n, func(e expr.Expr) (expr.Expr, bool, error) { return e, true, nil })
						if err != nil || copy == n {
							t.Fatal("actual accepted join did not reconstruct", err)
						}
						next := copy.(*plan.JoinNode)
						if next.RangeName != n.RangeName || &next.ColumnAliases[0] != &n.ColumnAliases[0] || next.Frame != n.Frame || next.ViewOrdinal != n.ViewOrdinal {
							t.Fatal("join reconstruction lost immutable accepted aliases/coordinates")
						}
					}
				}
				// Stored-view pointers may be shared, but each outer occurrence owns
				// a separate view scope with the actual checked owner principal.
				if query == "SELECT l.id,r.v FROM range_owner_view l JOIN range_owner_view r ON l.id=r.id" {
					children := map[any]bool{}
					for _, entry := range entries {
						if entry["Relation Name"] == "range_owner_view" {
							children[entry["Child Scope ID"]] = true
						}
					}
					if len(children) != 2 || children[nil] {
						t.Fatal("shared stored graph collapsed view occurrences", entries)
					}
				}
				if query == "WITH x AS MATERIALIZED (SELECT id FROM range_owner) SELECT (SELECT id FROM x LIMIT 1)" {
					producer := any(nil)
					for _, entry := range entries {
						if entry["Kind"] == "cte" {
							producer = entry["Producer Scope ID"]
						}
					}
					if producer == nil {
						t.Fatal("scalar CTE reader lost actual producer edge")
					}
					found := false
					for _, item := range document[0]["Embedded Range Table"].(map[string]any)["Scopes"].([]any) {
						scope := item.(map[string]any)
						if scope["Scope ID"] == producer {
							found = true
							if scope["Parent Scope ID"] != float64(1) {
								t.Fatal("producer ownership followed reader instead of definition frame", scope)
							}
						}
					}
					if !found {
						t.Fatal("missing actual definition-owned producer scope")
					}
				}
				if query == "INSERT INTO range_owner AS t(id,v) SELECT t.id,t.v FROM range_owner t RETURNING t.id" {
					var targetScope, sourceScope any
					for _, item := range document[0]["Embedded Range Table"].(map[string]any)["Scopes"].([]any) {
						scope := item.(map[string]any)
						for _, item := range scope["Entries"].([]any) {
							entry := item.(map[string]any)
							if entry["Range Name"] == "t" && entry["Role"] == "target" {
								targetScope = scope["Scope ID"]
							}
							if entry["Range Name"] == "t" && entry["Role"] == "source" {
								sourceScope = scope["Scope ID"]
							}
						}
					}
					if targetScope != float64(1) || sourceScope == nil || sourceScope == targetScope {
						t.Fatal("INSERT input shadowing collapsed command/query ownership", document)
					}
				}
			})
		})
	}
	t.Run("hidden-foreign-composite", func(t *testing.T) {
		foreignEngine, foreignSession, wrapper := explainExecutionFixture(t)
		explainRangeOwner(t, foreignEngine, foreignSession, "SELECT a.k FROM executed_explain_data a JOIN executed_explain_data b ON a.k=b.k", true, func(binding analyze.Binding, _ core.AuthorizationView, document []map[string]any) {
			entries := explainRangeEntries(t, document)
			for occurrence, leaf := range binding.QuerySources {
				found := false
				for _, entry := range entries {
					if entry["Kind"] == "relation" && entry["Occurrence"] == float64(occurrence) && entry["Relation OID"] == float64(leaf.Ref.Address.ObjectID) {
						found = true
					}
				}
				if !found {
					t.Fatal("selected composite erased accepted range source", occurrence, entries)
				}
			}
			selected := false
			var visit func(map[string]any)
			visit = func(node map[string]any) {
				if node["Foreign Operation"] == "join" && node["Plans"] == nil {
					selected = true
				}
				children, _ := node["Plans"].([]any)
				for _, child := range children {
					visit(child.(map[string]any))
				}
			}
			visit(document[0]["Plan"].(map[string]any))
			if !selected {
				t.Fatal("fixture did not genuinely hide join children")
			}
		})
		if wrapper.begins != 0 || wrapper.ends != 0 {
			t.Fatal("range capture entered foreign execution")
		}
	})
	explainRangeOwner(t, e, s, "SELECT id FROM range_owner", false, func(_ analyze.Binding, _ core.AuthorizationView, document []map[string]any) {
		for _, doc := range document {
			if _, exists := doc["Embedded Range Table"]; exists {
				t.Fatal("disabled range capture published facts")
			}
		}
	})
}
