package core_test

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func TestExplainRangeCorrelation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE range_review_corr(id integer)")
	cases := []struct {
		name, query                string
		outer                      map[string][]string
		tableoid, identityCaptures bool
	}{
		{"immediate", "SELECT a.id,(SELECT b.id FROM range_review_corr b WHERE b.id=a.id AND b.tableoid=a.tableoid OFFSET 0) FROM range_review_corr a", map[string][]string{"a": {}, "b": {"a"}}, true, false},
		{"ancestor-only", "SELECT a.id,(SELECT (SELECT c.id FROM range_review_corr c WHERE c.id=a.id AND c.tableoid=a.tableoid OFFSET 0) FROM range_review_corr b WHERE b.id=1 OFFSET 0) FROM range_review_corr a", map[string][]string{"a": {}, "b": {}, "c": {"a"}}, true, false},
		{"uncorrelated", "SELECT a.id,(SELECT (SELECT c.id FROM range_review_corr c WHERE c.id=1 OFFSET 0) FROM range_review_corr b WHERE b.id=1 OFFSET 0) FROM range_review_corr a", map[string][]string{"a": {}, "b": {}, "c": {}}, false, false},
		{"two-owners", "SELECT a.id,(SELECT (SELECT c.id FROM range_review_corr c WHERE c.id=a.id AND c.id=b.id OFFSET 0) FROM range_review_corr b WHERE b.id=a.id OFFSET 0) FROM range_review_corr a", map[string][]string{"a": {}, "b": {"a"}, "c": {"a", "b"}}, false, false},
		{"function-outer-column", "SELECT a.id,(SELECT g.n FROM generate_series(a.id,a.id) g(n)) FROM range_review_corr a", map[string][]string{"a": {}, "g": {"a"}}, false, false},
		{"function-join-identity-captures", "SELECT a.id,(SELECT g.n FROM range_review_corr b CROSS JOIN ((VALUES(1)) v(k) CROSS JOIN LATERAL unnest(ARRAY[b.tableoid]) g(n)) WHERE b.id=a.id LIMIT 1) FROM range_review_corr a", map[string][]string{"a": {}, "g": {"a"}}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var before []rangeReviewQuery
			explainRangeOwner(t, e, s, tc.query, true, func(binding analyze.Binding, _ core.AuthorizationView, document []map[string]any) {
				after := rangeReviewQueryOwners(binding.Query)
				if !reflect.DeepEqual(before, after) || len(after) != len(tc.outer) {
					t.Fatal("capture mutated/missed actual lexical query/frame/expression owners", before, after)
				}
				byFrame, byName := map[uint32]rangeReviewQuery{}, map[string]rangeReviewQuery{}
				for _, owner := range after {
					if owner.frame == 0 || owner.query == nil || owner.root == nil || owner.alias == "" {
						t.Fatal("fixture lost actual bound query ownership", owner)
					}
					if _, exists := byFrame[owner.frame]; exists {
						t.Fatal("fixture reused a lexical frame for distinct queries", after)
					}
					byFrame[owner.frame], byName[owner.alias] = owner, owner
				}
				entries := explainRangeEntries(t, document)
				scopes := map[string]map[string]any{}
				for name := range tc.outer {
					var entry map[string]any
					for _, candidate := range entries {
						if candidate["Range Name"] == name {
							if entry != nil {
								t.Fatal("fixture alias collapsed distinct actual owners", name)
							}
							entry = candidate
						}
					}
					if entry == nil {
						t.Fatal("actual lexical range owner absent", name, entries)
					}
					scopes[name] = explainRangeReviewScope(t, document, entry["Entry ID"])
				}
				sawOuterTableoid := false
				functionIdentities, joinIdentities := 0, 0
				for alias, owner := range byName {
					actualOwners := map[string]bool{}
					for _, coordinate := range owner.coordinates {
						if coordinate.scope != 1 {
							t.Fatal("fixture coordinate scope identity changed", coordinate)
						}
						resolved, ok := byFrame[coordinate.frame]
						if !ok {
							t.Fatal("retained lexical coordinate has no genuine query owner", coordinate, after)
						}
						if resolved.query == owner.query {
							continue
						}
						actualOwners[resolved.alias] = true
						if coordinate.tableoid {
							sawOuterTableoid = true
						}
					}
					for _, retained := range owner.captures {
						capture := retained.value
						resolved, ok := byFrame[capture.Frame]
						if !ok {
							t.Fatal("accepted capture lost its genuine frame owner", retained, after)
						}
						if capture.QueryOccurrence != 0 {
							name, exists := owner.sources[capture.QueryOccurrence]
							if !exists || name != "b" || capture.Name != name || capture.Slot != 0 || resolved.query != owner.query {
								t.Fatal("identity capture lost actual local source occurrence or became an outer owner", retained, owner)
							}
							switch retained.kind {
							case "function":
								functionIdentities++
							case "join":
								joinIdentities++
							default:
								t.Fatal("unknown accepted capture carrier", retained)
							}
						}
						if resolved.query != owner.query {
							actualOwners[resolved.alias] = true
						}
					}
					wantOwners := map[string]bool{}
					for _, name := range tc.outer[alias] {
						wantOwners[name] = true
					}
					if !reflect.DeepEqual(actualOwners, wantOwners) {
						t.Fatal("fixture did not distinguish direct parent/ancestor/no capture", alias, actualOwners, wantOwners)
					}
					scope := scopes[alias]
					if owner.parent == nil {
						if _, exists := scope["Parent Scope ID"]; exists {
							t.Fatal("root query invented lexical parent", scope)
						}
					} else {
						var parentName string
						for name, candidate := range byName {
							if candidate.query == owner.parent {
								parentName = name
							}
						}
						if parentName == "" || scope["Parent Scope ID"] != scopes[parentName]["Scope ID"] {
							t.Fatal("structural parent no longer follows accepted query ownership", owner, scope)
						}
					}
					value, exists := scope["Outer Scope IDs"]
					if len(wantOwners) == 0 {
						if exists {
							t.Fatal("nested child captures bubbled into an uncorrelated lexical owner", alias, scope)
						}
						continue
					}
					if !exists {
						t.Fatal("actual retained correlation omitted from scope projection", alias, owner.coordinates, owner.captures, scope)
					}
					wantIDs := make([]float64, 0, len(wantOwners))
					for name := range wantOwners {
						wantIDs = append(wantIDs, scopes[name]["Scope ID"].(float64))
					}
					slices.Sort(wantIDs)
					got := value.([]any)
					if len(got) != len(wantIDs) {
						t.Fatal("captured outer owner duplicated/omitted", got, wantIDs)
					}
					for i, want := range wantIDs {
						if got[i] != want {
							t.Fatal("outer IDs not exact direct owners in ascending order", got, wantIDs)
						}
					}
				}
				wantIdentities := 0
				if tc.identityCaptures {
					wantIdentities = 1
				}
				if sawOuterTableoid != tc.tableoid || functionIdentities != wantIdentities || joinIdentities != wantIdentities {
					t.Fatal("fixture failed to exercise genuine local function/join identity captures or outer tableoid owner", sawOuterTableoid, functionIdentities, joinIdentities, tc)
				}
			}, func(binding analyze.Binding) { before = rangeReviewQueryOwners(binding.Query) })
		})
	}
	t.Run("repeated-stored-coordinate-contexts", func(t *testing.T) {
		enginetest.SessionSQL(t, s, "CREATE VIEW range_review_corr_view AS SELECT a.id,(SELECT b.id FROM range_review_corr b WHERE b.id=a.id) AS captured FROM range_review_corr a")
		var before []rangeReviewQuery
		explainRangeOwner(t, e, s, "SELECT l.id,r.captured FROM range_review_corr_view l JOIN range_review_corr_view r ON l.id=r.id", true, func(binding analyze.Binding, _ core.AuthorizationView, document []map[string]any) {
			if len(binding.QuerySources) != 2 || binding.QuerySources[0].Relation.View != binding.QuerySources[1].Relation.View {
				t.Fatal("fixture did not reuse one genuine stored graph at two outer occurrences")
			}
			definition := stored.ViewOf(binding.QuerySources[0].Relation)
			after := rangeReviewQueryOwners(definition.Query())
			if !reflect.DeepEqual(before, after) || len(after) != 2 || after[0].frame != binding.Query.Root.(*plan.ProjectNode).Frame {
				t.Fatal("fixture missed stored/outer frame collision or capture mutated stored graph", before, after)
			}
			entries := explainRangeEntries(t, document)
			owners := map[any]bool{}
			for _, entry := range entries {
				if entry["Relation Name"] == "range_review_corr_view" {
					owners[entry["Child Scope ID"]] = true
				}
			}
			if len(owners) != 2 || owners[nil] {
				t.Fatal("repeated view collapsed contextual lexical owners", entries)
			}
			counts := map[any]int{}
			for _, item := range document[0]["Embedded Range Table"].(map[string]any)["Scopes"].([]any) {
				scope := item.(map[string]any)
				if owners[scope["Scope ID"]] {
					if _, exists := scope["Outer Scope IDs"]; exists {
						t.Fatal("child's correlation bubbled into stored query owner", scope)
					}
				}
				for _, item := range scope["Entries"].([]any) {
					entry := item.(map[string]any)
					if entry["Kind"] == "relation" && entry["Range Name"] == "b" {
						parent := scope["Parent Scope ID"]
						if !owners[parent] || !reflect.DeepEqual(scope["Outer Scope IDs"], []any{parent}) {
							t.Fatal("stored scalar capture used global frame owner or sibling view context", scope, owners)
						}
						counts[parent]++
					}
				}
			}
			for owner := range owners {
				if counts[owner] != 1 {
					t.Fatal("missing/duplicated context-specific stored scalar owner", owner, counts)
				}
			}
		}, func(binding analyze.Binding) {
			if len(binding.QuerySources) != 0 {
				before = rangeReviewQueryOwners(stored.ViewOf(binding.QuerySources[0].Relation).Query())
			}
		})
	})
}

type rangeReviewCoordinate struct {
	scope, frame uint32
	tableoid     bool
}
type rangeReviewCapture struct {
	kind  string
	value plan.FunctionCorrelation
}
type rangeReviewQuery struct {
	query, parent *plan.Query
	root          plan.Node
	frame         uint32
	alias         string
	coordinates   []rangeReviewCoordinate
	captures      []rangeReviewCapture
	sources       map[int]string
}

// This owner probe stops at child-query boundaries when reading coordinates.
// The query graph itself, not SQL aliases or syntax nesting depth, owns frames.
func rangeReviewQueryOwners(query *plan.Query) []rangeReviewQuery {
	var owners []rangeReviewQuery
	var collect func(*plan.Query, *plan.Query)
	collect = func(query, parent *plan.Query) {
		if query == nil {
			return
		}
		owner := rangeReviewQuery{query: query, parent: parent, root: query.Root, sources: make(map[int]string)}
		if project, ok := query.Root.(*plan.ProjectNode); ok {
			owner.frame = project.Frame
		}
		var children []*plan.Query
		var walk func(plan.Node)
		walk = func(node plan.Node) {
			if node == nil {
				return
			}
			switch n := node.(type) {
			case *plan.SourceNode:
				owner.alias = n.RangeName
				owner.sources[n.Occurrence+1] = n.RangeName
			case *plan.FunctionFromNode:
				owner.alias = n.RangeName
				for _, capture := range n.Captures {
					owner.captures = append(owner.captures, rangeReviewCapture{kind: "function", value: capture})
				}
			case *plan.JoinNode:
				for _, capture := range n.Captures {
					owner.captures = append(owner.captures, rangeReviewCapture{kind: "join", value: capture})
				}
			case *plan.DerivedNode, *plan.WorktableScanNode:
				return
			}
			plan.QueryExpressions(node, func(expression expr.Expr) {
				plan.WalkExpression(expression, func(expression expr.Expr) {
					switch e := expression.(type) {
					case plan.ColumnExpr:
						owner.coordinates = append(owner.coordinates, rangeReviewCoordinate{e.Scope, e.Frame, false})
					case plan.TableOIDExpr:
						owner.coordinates = append(owner.coordinates, rangeReviewCoordinate{e.Scope, e.Frame, true})
					case plan.SubqueryExpr:
						if e.QueryPlan != nil && !slices.Contains(children, e.QueryPlan) {
							children = append(children, e.QueryPlan)
						}
					}
				})
			})
			left, right := plan.QueryInputs(node)
			walk(left)
			walk(right)
		}
		walk(query.Root)
		owners = append(owners, owner)
		for _, child := range children {
			collect(child, query)
		}
	}
	collect(query, nil)
	return owners
}
