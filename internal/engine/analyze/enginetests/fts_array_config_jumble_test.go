package enginetests

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestFTSArrayConfigStatementJumble(t *testing.T) {
	e := engine.New()
	for _, source := range []string{
		"SELECT get_current_ts_config()",
		"SELECT array_to_tsvector(ARRAY['b','a','a'])",
	} {
		t.Run(source, func(t *testing.T) {
			ast, binding := pgssAnalyzedBinding(t, e, source)
			project, ok := binding.Query.Root.(*plan.ProjectNode)
			if !ok || len(project.Expressions) != 1 {
				t.Fatal("missing bound scalar projection")
			}
			node := project.Expressions[0]
			switch node.(type) {
			case functions.FTSCurrentConfig, functions.FTSVector:
			default:
				t.Fatalf("missing real array/config provider: %T", node)
			}
			if _, ok := node.(expr.StatementJumbler); !ok {
				t.Fatal("provider lacks canonical identity projection")
			}
			before := analyze.JumbleStatement(ast, binding)
			if before.QueryID() == 0 {
				t.Fatal("empty canonical statement identity")
			}
			composite := node.(expr.Composite)
			rebuilt, err := composite.Rebuild(expr.InfoOf(node), composite.Children())
			if err != nil {
				t.Fatal(err)
			}
			projectCopy := *project
			projectCopy.Expressions = []expr.Expr{rebuilt}
			queryCopy := *binding.Query
			queryCopy.Root = &projectCopy
			bindingCopy := *binding
			bindingCopy.Query = &queryCopy
			if after := analyze.JumbleStatement(ast, &bindingCopy); after.QueryID() != before.QueryID() {
				t.Fatal("reconstruction changed canonical scalar identity")
			}
		})
	}
	plain := pgssAnalyzedJumble(t, e, "SELECT get_current_ts_config()")
	qualified := pgssAnalyzedJumble(t, e, "SELECT pg_catalog.get_current_ts_config() AS cfg")
	literal := pgssAnalyzedJumble(t, e, "SELECT 'simple'::regconfig")
	if plain.QueryID() != qualified.QueryID() || plain.QueryID() == literal.QueryID() {
		t.Fatal("current configuration lost actual routine identity or became a captured constant")
	}
	t.Run("constructor-normalization", func(t *testing.T) {
		type parameterSite struct {
			number, offset, character int
			typ                       sqltype.ID
		}
		identities := make(map[string]int64)
		for _, tc := range []struct {
			source, normalized, group string
			locations                 []int
			lists                     [][2]int
			highest                   int
			parameters                []parameterSite
		}{
			{
				source:     "SELECT array_to_tsvector('{b,a,a}'::text[])",
				normalized: "SELECT array_to_tsvector($1::text[])",
				group:      "text-array-literal", locations: []int{25},
			},
			{
				source:     "SELECT array_to_tsvector('{rats,The}'::text[])",
				normalized: "SELECT array_to_tsvector($1::text[])",
				group:      "text-array-literal", locations: []int{25},
			},
			{
				source:     "SELECT array_to_tsvector(ARRAY['b','a','a'])",
				normalized: "SELECT array_to_tsvector(ARRAY[$1 /*, ... */])",
				group:      "array-list", lists: [][2]int{{31, 39}},
			},
			{
				source:     "SELECT array_to_tsvector(ARRAY['rats','The'])",
				normalized: "SELECT array_to_tsvector(ARRAY[$1 /*, ... */])",
				group:      "array-list", lists: [][2]int{{31, 38}},
			},
			{
				source:     "SELECT array_to_tsvector($1::text[]), 'sentinel'::text",
				normalized: "SELECT array_to_tsvector($1::text[]), $2::text",
				locations:  []int{38}, highest: 1,
				parameters: []parameterSite{{1, 25, 26, 1009}},
			},
			{
				source:     "SELECT array_to_tsvector($1::text[]), $2::text[]",
				normalized: "SELECT array_to_tsvector($1::text[]), $2::text[]",
				group:      "parameter-order-1", highest: 2,
				parameters: []parameterSite{{1, 25, 26, 1009}, {2, 38, 39, 1009}},
			},
			{
				source:     "SELECT array_to_tsvector($2::text[]), $1::text[]",
				normalized: "SELECT array_to_tsvector($2::text[]), $1::text[]",
				group:      "parameter-order-2", highest: 2,
				parameters: []parameterSite{{2, 25, 26, 1009}, {1, 38, 39, 1009}},
			},
		} {
			t.Run(tc.source, func(t *testing.T) {
				ast, binding := pgssAnalyzedBinding(t, e, tc.source)
				jumble := analyze.JumbleStatement(ast, binding)
				if jumble.HighestParameter != tc.highest || !reflect.DeepEqual(jumble.Locations, tc.locations) || !reflect.DeepEqual(jumble.Lists, tc.lists) || len(jumble.UtilityLocations) != 0 {
					t.Fatalf("normalization provenance: highest=%d locations=%v lists=%v utility=%v; want %d %v %v", jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, tc.highest, tc.locations, tc.lists)
				}
				normalized, raw := parser.NormalizeStatementText(tc.source, 0, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
				if normalized != tc.normalized || raw != tc.source {
					t.Fatalf("normalized=%q raw=%q; want %q and %q", normalized, raw, tc.normalized, tc.source)
				}
				var parameters []parameterSite
				plan.QueryExpressions(binding.Query.Root, func(node expr.Expr) {
					plan.WalkExpression(node, func(child expr.Expr) {
						if parameter, ok := child.(plan.ParameterExpr); ok {
							pos := parameter.SourcePosition()
							parameters = append(parameters, parameterSite{int(parameter.Number), pos.Offset, pos.Character, parameter.ResultUsage().Type.ID()})
						}
					})
				})
				if !reflect.DeepEqual(parameters, tc.parameters) {
					t.Fatalf("bound parameter identity/location=%v; want %v", parameters, tc.parameters)
				}
				if tc.group != "" {
					if before, exists := identities[tc.group]; exists && before != jumble.QueryID() {
						t.Fatal("literal payload or squashable list length changed constructor identity")
					}
					identities[tc.group] = jumble.QueryID()
				}
			})
		}
		if identities["parameter-order-1"] == identities["parameter-order-2"] {
			t.Fatal("constructor canonical identity omitted written parameter numbers")
		}
	})
	t.Run("current-config-setting-independent", func(t *testing.T) {
		s := enginetest.StartSession(t, e, nil)
		const source = "SELECT get_current_ts_config()"
		ast := enginetest.Statement(t, source)
		english := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "english"})
		if english.ObjectID == 0 || english.ObjectID == 3748 {
			t.Fatal("actual English descriptor must distinguish the two settings", english)
		}
		// Bind the same syntax through the existing Describe sequence, but retain
		// this real session's operation so SET changes are not lost to a new owner.
		bound := func() (analyze.StatementJumble, uint32) {
			t.Helper()
			op, err := s.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			op.OriginalSQL, op.SQLAvailable = source, true
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			if _, err := scope.Describe(ast); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			var binding analyze.Binding
			if err == nil {
				binding, err = core.Describe(op.Context(), e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			project, ok := binding.Query.Root.(*plan.ProjectNode)
			if !ok || len(project.Expressions) != 1 {
				t.Fatal("missing current-config bound scalar")
			}
			current, ok := project.Expressions[0].(functions.FTSCurrentConfig)
			if !ok || current.IntrinsicVolatility() != expr.Stable || len(current.Children()) != 0 {
				t.Fatal("current-config call ceased to be a symbolic stable scalar")
			}
			// The actual resolver must also see the changed setting, independently
			// of the canonical identity. No allocated OID is a fingerprint golden.
			value, err := core.HostOf(scope).TextSearch().CurrentTextSearchConfig(op.Context(), current.ResultUsage().Type)
			if err != nil {
				t.Fatal(err)
			}
			oid, err := value.RegTSOID()
			if err != nil {
				t.Fatal(err)
			}
			return analyze.JumbleStatement(ast, &binding), oid
		}
		for _, tc := range []struct {
			name string
			oid  uint32
		}{
			{"simple", 3748}, {"english", uint32(english.ObjectID)}, {"simple", 3748},
		} {
			enginetest.SessionSQL(t, s, "SET default_text_search_config='"+tc.name+"'")
			jumble, actualOID := bound()
			if actualOID != tc.oid || jumble.QueryID() != plain.QueryID() {
				t.Fatalf("setting %s: resolved OID=%d want %d, symbolic identity=%d want %d", tc.name, actualOID, tc.oid, jumble.QueryID(), plain.QueryID())
			}
			if jumble.HighestParameter != 0 || len(jumble.Locations) != 0 || len(jumble.Lists) != 0 || len(jumble.UtilityLocations) != 0 {
				t.Fatal("current setting became a normalization constant or parameter", jumble)
			}
			normalized, raw := parser.NormalizeStatementText(source, 0, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
			if normalized != source || raw != source {
				t.Fatal("symbolic current-config source changed", normalized, raw)
			}
		}
	})
}
