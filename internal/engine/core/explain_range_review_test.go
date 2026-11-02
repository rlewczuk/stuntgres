package core_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

func TestExplainRangeRewriteCTE(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE ROLE range_owner_rule", "CREATE ROLE range_owner_caller",
		"GRANT USAGE ON SCHEMA public TO range_owner_rule,range_owner_caller",
		"CREATE TABLE range_owner_original(id integer,v text)", "CREATE TABLE range_owner_actual(id integer,v text)",
		"ALTER TABLE range_owner_original OWNER TO range_owner_rule",
		"GRANT INSERT(id,v),SELECT(id,v) ON range_owner_original TO range_owner_caller",
		"GRANT INSERT(id,v),SELECT(id,v) ON range_owner_actual TO range_owner_rule",
		"CREATE RULE range_owner_rewrite AS ON INSERT TO range_owner_original DO INSTEAD INSERT INTO range_owner_actual(id,v) VALUES(NEW.id,NEW.v) RETURNING id,v",
		"SET ROLE range_owner_caller",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	for _, query := range []string{
		"WITH changed AS (INSERT INTO range_owner_original VALUES(1,NULL) RETURNING id,v) SELECT id,v FROM changed",
		"WITH changed AS (INSERT INTO range_owner_original VALUES(1,NULL) RETURNING id,v), readers AS MATERIALIZED (SELECT id,v FROM changed) SELECT id,v FROM readers",
	} {
		t.Run(query, func(t *testing.T) {
			var accepted *view.RuleCTECommand
			var originalHeader, actualHeader analyze.Binding
			var originalRows, actualRows [][]expr.Expr
			var originalReturning, actualReturning []expr.Expr
			var producer *plan.CTEDefinition
			explainRangeOwner(t, e, s, query, true, func(binding analyze.Binding, _ core.AuthorizationView, document []map[string]any) {
				rewrites := view.RuleCTEsOf(binding.Rule.CTEs)
				if len(rewrites) != 1 || accepted == nil || rewrites[producer] != accepted || accepted.Definition != producer {
					t.Fatal("fixture did not preserve a genuine normalized CTE provider/command tuple", rewrites)
				}
				original, ok := analyze.MutationBinding(producer)
				if !ok {
					t.Fatal("original CTE mutation provider disappeared")
				}
				caller, err := accepted.Caller.Access().Roles.Descriptor(accepted.Caller.Access().State.CurrentUser)
				if err != nil {
					t.Fatal(err)
				}
				authority := accepted.Command.Auth.Access()
				actualRole, err := authority.Roles.Descriptor(authority.State.CurrentUser)
				if err != nil {
					t.Fatal(err)
				}
				if caller.Name != "range_owner_caller" || actualRole.Name != "range_owner_rule" || caller.Ref == actualRole.Ref || original.Ref.Address == accepted.Command.Binding.Ref.Address {
					t.Fatal("fixture failed to distinguish caller/actual target and authority", caller, actualRole)
				}
				entries := explainRangeEntries(t, document)
				logical := explainRangeReviewRelation(t, entries, original.Ref.Address.ObjectID)
				actual := explainRangeReviewRelation(t, entries, accepted.Command.Binding.Ref.Address.ObjectID)
				explainRangeReviewDemands(t, logical, caller.Name, []rangeReviewDemand{{"INSERT", []string{"id", "v"}, false}, {"SELECT", []string{"id", "v"}, false}})
				explainRangeReviewDemands(t, actual, actualRole.Name, []rangeReviewDemand{{"INSERT", []string{"id", "v"}, false}, {"SELECT", []string{"id", "v"}, false}})
				logicalScope := explainRangeReviewScope(t, document, logical["Entry ID"])
				actualScope := explainRangeReviewScope(t, document, actual["Entry ID"])
				if logicalScope["Kind"] != "caller" || actualScope["Kind"] != "rule" || logicalScope["Scope ID"] == actualScope["Scope ID"] || logicalScope["Parent Scope ID"] != actualScope["Parent Scope ID"] {
					t.Fatal("rewritten producer confused provider and executed ownership", logicalScope, actualScope)
				}
				foundProducer, foundReader := false, false
				for _, item := range document[0]["Embedded Range Table"].(map[string]any)["Scopes"].([]any) {
					scope := item.(map[string]any)
					if scope["Scope ID"] == actualScope["Parent Scope ID"] && scope["Kind"] == "cte" {
						foundProducer = true
					}
				}
				for _, entry := range entries {
					if entry["Kind"] == "cte" && entry["Range Name"] == "changed" && entry["Producer Scope ID"] == actualScope["Parent Scope ID"] {
						foundReader = true
					}
				}
				if !foundProducer || !foundReader {
					t.Fatal("actual rewrite lost original producer/reader identity", document)
				}
				if !reflect.DeepEqual(*original, originalHeader) || !reflect.DeepEqual(accepted.Command.Binding, actualHeader) || !reflect.DeepEqual(original.Expressions, originalRows) || !reflect.DeepEqual(accepted.Command.Binding.Expressions, actualRows) || !reflect.DeepEqual(original.Returning.Expressions, originalReturning) || !reflect.DeepEqual(accepted.Command.Binding.Returning.Expressions, actualReturning) {
					t.Fatal("range capture mutated accepted original/rewritten graphs or expression backing")
				}
			}, func(binding analyze.Binding) {
				for definition, rewrite := range view.RuleCTEsOf(binding.Rule.CTEs) {
					producer, accepted = definition, rewrite
					if mutation, ok := analyze.MutationBinding(definition); ok {
						originalHeader = *mutation
						originalRows = rangeReviewRows(mutation.Expressions)
						if mutation.Returning != nil {
							originalReturning = slices.Clone(mutation.Returning.Expressions)
						}
					}
					actualHeader = rewrite.Command.Binding
					actualRows = rangeReviewRows(rewrite.Command.Binding.Expressions)
					if rewrite.Command.Binding.Returning != nil {
						actualReturning = slices.Clone(rewrite.Command.Binding.Returning.Expressions)
					}
				}
			})
		})
	}
}

func TestExplainRangeViewDemandContexts(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE ROLE range_view_context_owner", "CREATE ROLE range_view_context_actor",
		"GRANT USAGE ON SCHEMA public TO range_view_context_owner,range_view_context_actor",
		"CREATE TABLE range_view_context_base(id integer,v text)",
		"GRANT SELECT(id,v),UPDATE(v) ON range_view_context_base TO range_view_context_owner",
		"CREATE VIEW range_view_context_plain AS SELECT id,v FROM range_view_context_base",
		"CREATE VIEW range_view_context_marked AS SELECT id,v FROM range_view_context_base FOR UPDATE",
		"CREATE VIEW range_view_context_oid_marked AS SELECT id,v,tableoid AS origin FROM range_view_context_base FOR UPDATE",
		"ALTER VIEW range_view_context_plain OWNER TO range_view_context_owner", "ALTER VIEW range_view_context_marked OWNER TO range_view_context_owner",
		"ALTER VIEW range_view_context_oid_marked OWNER TO range_view_context_owner",
		"GRANT SELECT(id,v) ON range_view_context_plain,range_view_context_marked,range_view_context_oid_marked TO range_view_context_actor",
		"SET ROLE range_view_context_actor",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	for _, tc := range []struct {
		query, name                        string
		analysisOnly, validation, tableoid bool
	}{
		{"WITH unused AS (SELECT id FROM range_view_context_plain) SELECT 1", "range_view_context_plain", true, false, false},
		{"WITH unused AS (SELECT id FROM range_view_context_marked) SELECT 1", "range_view_context_marked", true, true, false},
		{"SELECT id FROM range_view_context_marked", "range_view_context_marked", false, true, false},
		{"WITH unused AS (SELECT id FROM range_view_context_oid_marked) SELECT 1", "range_view_context_oid_marked", true, true, true},
		{"SELECT id FROM range_view_context_oid_marked", "range_view_context_oid_marked", false, true, true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			if tc.tableoid && !tc.analysisOnly {
				enginetest.SessionSQL(t, s, "RESET ROLE")
				enginetest.SessionSQL(t, s, "GRANT SELECT ON range_view_context_base TO range_view_context_owner")
				enginetest.SessionSQL(t, s, "SET ROLE range_view_context_actor")
			}
			var graph *plan.Query
			var leaves []stored.ViewLeaf
			explainRangeOwner(t, e, s, tc.query, true, func(binding analyze.Binding, auth core.AuthorizationView, document []map[string]any) {
				if len(binding.QuerySources) != 1 {
					t.Fatal("fixture did not bind one genuine view occurrence", binding.QuerySources)
				}
				source := binding.QuerySources[0]
				definition := stored.ViewOf(source.Relation)
				if source.Relation.Name() != tc.name || source.AnalysisOnly != tc.analysisOnly || stored.ViewHasRowMarks(source.Relation, auth.Catalog) != tc.validation {
					t.Fatal("fixture missed authoritative analysis/mark-validation gate", source, tc)
				}
				if err := auth.CheckViewRowMarks(source.Relation, source.Mark); err != nil {
					t.Fatal("genuine view authority failed before comparing detached demands", err)
				}
				if len(definition.Sources()) != 1 {
					t.Fatal("stored fixture lost its single actual base occurrence", definition.Sources())
				}
				if definition.Sources()[0].SystemAttributeRead != tc.tableoid {
					t.Fatal("fixture lost actual tableoid read-channel fact", definition.Sources())
				}
				baseRelation, ok := auth.Catalog.Relation(definition.Sources()[0].Ref.Address)
				if !ok || baseRelation.Name() != "range_view_context_base" || !reflect.DeepEqual(definition.Sources()[0].Reads, baseRelation.Attributes) {
					t.Fatal("stored definition lost its complete real id/v base read set", definition.Sources())
				}
				role, err := auth.Roles.Descriptor(source.Relation.Owner)
				if err != nil || role.Name != "range_view_context_owner" {
					t.Fatal("missing authentic stored effective principal", role, err)
				}
				entries := explainRangeEntries(t, document)
				outer := explainRangeReviewRelation(t, entries, source.Ref.Address.ObjectID)
				base := explainRangeReviewRelation(t, entries, definition.Sources()[0].Ref.Address.ObjectID)
				if len(entries) != 2 {
					t.Fatal("view fixture invented/duplicated range owners", entries)
				}
				for _, entry := range []map[string]any{outer, base} {
					value, exists := entry["Analysis Only"]
					if tc.analysisOnly && (!exists || value != true) || !tc.analysisOnly && exists {
						t.Fatal("analysis-only ancestry was not retained on actual stored owner", entry)
					}
				}
				outerWants, baseWants := []rangeReviewDemand{}, []rangeReviewDemand{}
				if !tc.analysisOnly {
					outerWants = append(outerWants, rangeReviewDemand{"SELECT", []string{"id"}, false})
				}
				if tc.tableoid && !tc.analysisOnly {
					baseWants = append(baseWants, rangeReviewDemand{"SELECT", []string{}, true})
				}
				if tc.validation {
					baseWants = append(baseWants, rangeReviewDemand{"SELECT", []string{"id", "v"}, false}, rangeReviewDemand{"UPDATE", []string{}, false})
				}
				explainRangeReviewDemands(t, outer, "range_view_context_actor", outerWants)
				explainRangeReviewDemands(t, base, role.Name, baseWants)
				if definition.Query() != graph || !reflect.DeepEqual(definition.Sources(), leaves) {
					t.Fatal("projection changed actual stored query/source demands")
				}
			}, func(binding analyze.Binding) {
				if len(binding.QuerySources) != 0 {
					definition := stored.ViewOf(binding.QuerySources[0].Relation)
					graph, leaves = definition.Query(), slices.Clone(definition.Sources())
				}
			})
		})
	}
}

func rangeReviewRows(rows [][]expr.Expr) [][]expr.Expr {
	if rows == nil {
		return nil
	}
	copy := make([][]expr.Expr, len(rows))
	for i, row := range rows {
		copy[i] = slices.Clone(row)
	}
	return copy
}

type rangeReviewDemand struct {
	privilege string
	columns   []string
	whole     bool
}

func explainRangeReviewDemands(t *testing.T, entry map[string]any, principal string, wants []rangeReviewDemand) {
	t.Helper()
	key := func(principal, privilege string, columns []string, whole bool) string {
		return fmt.Sprintf("%q/%q/%q/%t", principal, privilege, columns, whole)
	}
	got, want := map[string]int{}, map[string]int{}
	for _, demand := range wants {
		want[key(principal, demand.privilege, demand.columns, demand.whole)]++
	}
	for _, item := range entry["Required Permissions"].([]any) {
		d := item.(map[string]any)
		columns := make([]string, len(d["Columns"].([]any)))
		for i, name := range d["Columns"].([]any) {
			columns[i] = name.(string)
		}
		got[key(d["Principal"].(string), d["Privilege"].(string), columns, d["Whole Relation"].(bool))]++
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("actual-owner demand multiset differs", got, want, entry)
	}
}

func explainRangeReviewRelation(t *testing.T, entries []map[string]any, oid ids.OID) map[string]any {
	t.Helper()
	var found map[string]any
	for _, entry := range entries {
		if entry["Kind"] == "relation" && entry["Relation OID"] == float64(oid) {
			if found != nil {
				t.Fatal("one actual fixture relation was duplicated", oid, entries)
			}
			found = entry
		}
	}
	if found == nil {
		t.Fatal("actual accepted relation missing from projection", oid, entries)
	}
	return found
}

func explainRangeReviewScope(t *testing.T, document []map[string]any, entryID any) map[string]any {
	t.Helper()
	var found map[string]any
	for _, doc := range document {
		group, ok := doc["Embedded Range Table"].(map[string]any)
		if !ok {
			continue
		}
		for _, item := range group["Scopes"].([]any) {
			scope := item.(map[string]any)
			for _, item := range scope["Entries"].([]any) {
				if item.(map[string]any)["Entry ID"] == entryID {
					if found != nil {
						t.Fatal("entry duplicated between actual owner scopes", entryID)
					}
					found = scope
				}
			}
		}
	}
	if found == nil {
		t.Fatal("missing entry ownership scope", entryID)
	}
	return found
}
