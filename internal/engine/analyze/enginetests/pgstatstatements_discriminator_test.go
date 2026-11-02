package enginetests

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// These are pre-refactor relationship baselines, not a claimed SQL failure
// caused by reflection. The implementation constraint needs source review too.
func TestPgStatStatementsFingerprintDiscriminators(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE pgss_discriminator(k integer,b boolean)")
	for _, tc := range []struct {
		name, left, right string
		same              bool
	}{
		{"null_truth", "SELECT b IS NULL FROM pgss_discriminator", "SELECT b IS TRUE FROM pgss_discriminator", false},
		{"null_unknown", "SELECT b IS NULL FROM pgss_discriminator", "SELECT b IS UNKNOWN FROM pgss_discriminator", false},
		{"null_spelling", "SELECT b IS NULL FROM pgss_discriminator", "SELECT /* 雪 */ b ISNULL AS other_name FROM pgss_discriminator", true},
		{"truth_position", "SELECT b IS TRUE FROM pgss_discriminator", "SELECT /* 雪 */ b IS TRUE AS other_name FROM pgss_discriminator", true},
		{"case_literals", "SELECT CASE WHEN b THEN 1 ELSE 2 END FROM pgss_discriminator", "SELECT CASE WHEN b THEN 3 ELSE 4 END FROM pgss_discriminator", true},
		{"distinct_query", "SELECT b FROM pgss_discriminator", "SELECT DISTINCT b FROM pgss_discriminator", false},
		{"values_query", "SELECT 1", "VALUES(1)", false},
		{"mutation_root", "UPDATE pgss_discriminator SET k=1 WHERE k=2", "DELETE FROM pgss_discriminator WHERE k=2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left := pgssAnalyzedJumble(t, e, tc.left)
			right := pgssAnalyzedJumble(t, e, tc.right)
			if left.QueryID() == 0 || right.QueryID() == 0 || (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("selected structural relationship: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
}

func TestPgStatStatementsFingerprintPrivateCarriers(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		name, left, right, normalized string
	}{
		{"commit_timestamp", "SELECT pg_xact_commit_timestamp('3'::xid)", "SELECT pg_xact_commit_timestamp('4'::xid)", "SELECT pg_xact_commit_timestamp($1::xid)"},
		{"snapshot", "SELECT pg_snapshot_xmin('1:2:'::pg_snapshot)", "SELECT pg_snapshot_xmin('2:3:'::pg_snapshot)", "SELECT pg_snapshot_xmin($1::pg_snapshot)"},
		{"publication", "SELECT * FROM pg_get_publication_tables('first')", "SELECT * FROM pg_get_publication_tables('second')", "SELECT * FROM pg_get_publication_tables($1)"},
		{"json_document", "SELECT JSON_QUERY('{}','$')", "SELECT JSON_QUERY('[]','$')", "SELECT JSON_QUERY($1,$2)"},
		{"transaction_identity", "SELECT pg_current_xact_id()", "SELECT /* 雪 */ pg_catalog.pg_current_xact_id() AS other_name", "SELECT pg_current_xact_id()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left := pgssAnalyzedJumble(t, e, tc.left)
			right := pgssAnalyzedJumble(t, e, tc.right)
			if left.QueryID() == 0 || left.QueryID() != right.QueryID() {
				t.Fatal("private callable carrier lost ignored-literal/name/position relationship")
			}
			normalized, raw := parser.NormalizeStatementText(tc.left, 0, left.HighestParameter, left.Locations, left.Lists, left.UtilityLocations, parser.Options{StandardConformingStrings: true})
			if normalized != tc.normalized || raw != tc.left {
				t.Fatalf("private carrier source: normalized=%q raw=%q, want %q and %q", normalized, raw, tc.normalized, tc.left)
			}
		})
	}
}

func TestPgStatStatementsFingerprintEmbeddedCarriers(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION fuzzystrmatch")
	// These three installed wrappers inherit Eval from their function kernel.
	// Reconstruction must preserve the genuine wrapper, procedure and reference,
	// rather than silently exposing the bare kernel's projection.
	capture := func(t *testing.T, node expr.Expr) (*catalog.Procedure, ids.ObjectRef) {
		t.Helper()
		switch n := node.(type) {
		case plan.InstalledMetaphoneExpr:
			return n.Procedure, n.Ref
		case plan.InstalledLevenshteinExpr:
			return n.Procedure, n.Ref
		case plan.InstalledSoundexExpr:
			return n.Procedure, n.Ref
		default:
			t.Fatalf("lost installed carrier: %T", node)
			return nil, ids.ObjectRef{}
		}
	}
	seen := make(map[int64]bool)
	for _, tc := range []struct {
		source, normalized string
	}{
		{"SELECT metaphone('GUMBO',4)", "SELECT metaphone($1,$2)"},
		{"SELECT levenshtein('kitten','sitting')", "SELECT levenshtein($1,$2)"},
		{"SELECT soundex('John')", "SELECT soundex($1)"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			ast, binding := pgssAnalyzedBinding(t, e, tc.source)
			project, ok := binding.Query.Root.(*plan.ProjectNode)
			if !ok || len(project.Expressions) != 1 {
				t.Fatal("missing genuine scalar projection")
			}
			original := project.Expressions[0]
			procedure, ref := capture(t, original)
			if procedure == nil || ref.Address.ObjectID == 0 {
				t.Fatal("missing installed declaration authority")
			}
			composite, ok := original.(expr.Composite)
			if !ok {
				t.Fatal("installed carrier lost lexical reconstruction")
			}
			rebuilt, err := expr.Substitute(composite, expr.InfoOf(original), composite.Children())
			if err != nil {
				t.Fatal(err)
			}
			newProcedure, newRef := capture(t, rebuilt)
			if newProcedure != procedure || newRef != ref {
				t.Fatal("reconstruction changed genuine installed authority")
			}
			projectCopy := *project
			projectCopy.Expressions = []expr.Expr{rebuilt}
			queryCopy := *binding.Query
			queryCopy.Root = &projectCopy
			bindingCopy := *binding
			bindingCopy.Query = &queryCopy
			before := analyze.JumbleStatement(ast, binding)
			after := analyze.JumbleStatement(ast, &bindingCopy)
			if before.QueryID() != after.QueryID() || seen[before.QueryID()] {
				t.Fatal("installed reconstruction or distinct callable identity changed")
			}
			seen[before.QueryID()] = true
			for _, jumble := range []analyze.StatementJumble{before, after} {
				normalized, raw := parser.NormalizeStatementText(tc.source, 0, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
				if normalized != tc.normalized || raw != tc.source {
					t.Fatalf("installed carrier source: normalized=%q raw=%q, want %q and %q", normalized, raw, tc.normalized, tc.source)
				}
			}
		})
	}
}

func TestPgStatStatementsFingerprintRowComparison(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE pgss_row_cmp(k integer)")
	// REL_18_6 RowCompareExpr selects cmptype for these four ordering kinds.
	// Native row equality/distinct modes share the carrier; their existing
	// ignored-literal and source-position relationships must remain intact too.
	operators := []string{"<", "<=", ">", ">=", "=", "<>", "IS DISTINCT FROM", "IS NOT DISTINCT FROM"}
	identities := make([]int64, 4)
	for i, operator := range operators {
		t.Run("same_kind/"+operator, func(t *testing.T) {
			leftSource := "SELECT (k,k) " + operator + " (1,2) FROM pgss_row_cmp"
			rightSource := "SELECT /* row 雪 */ (k,k) " + operator + " (3,4) AS ignored FROM pgss_row_cmp"
			ast, binding := pgssAnalyzedBinding(t, e, leftSource)
			project, ok := binding.Query.Root.(*plan.ProjectNode)
			if !ok || len(project.Expressions) != 1 {
				t.Fatal("missing genuine row predicate projection")
			}
			row, ok := project.Expressions[0].(plan.RowComparisonExpr)
			if !ok || row.Operator != operator || len(row.Comparisons) != 2 || row.Info.CallRoutineReference() != nil {
				t.Fatal("missing bound outer comparison kind or unexpected outer routine")
			}
			left := analyze.JumbleStatement(ast, binding)
			right := pgssAnalyzedJumble(t, e, rightSource)
			if left.QueryID() == 0 || left.QueryID() != right.QueryID() {
				t.Fatal("same row comparison kind lost literal/label/position equivalence")
			}
			if i < len(identities) {
				identities[i] = left.QueryID()
			}
			for _, source := range []struct {
				text, want string
				jumble     analyze.StatementJumble
			}{
				{leftSource, "SELECT (k,k) " + operator + " ($1,$2) FROM pgss_row_cmp", left},
				{rightSource, "SELECT /* row 雪 */ (k,k) " + operator + " ($1,$2) AS ignored FROM pgss_row_cmp", right},
			} {
				normalized, raw := parser.NormalizeStatementText(source.text, 0, source.jumble.HighestParameter, source.jumble.Locations, source.jumble.Lists, source.jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
				if normalized != source.want || raw != source.text {
					t.Fatalf("row comparison source: normalized=%q raw=%q, want %q and %q", normalized, raw, source.want, source.text)
				}
			}
		})
	}
	for i, left := range identities {
		for j := i + 1; j < len(identities); j++ {
			t.Run("distinct_kind/"+operators[i]+"/"+operators[j], func(t *testing.T) {
				if left == 0 || identities[j] == 0 || left == identities[j] {
					t.Fatalf("row comparison kinds %s and %s share or lack identity", operators[i], operators[j])
				}
			})
		}
	}
}

func TestPgStatStatementsFingerprintRowComparisonIgnoredPairIdentity(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE pgss_row_pair_narrow(k integer)")
	enginetest.Run(t, e, "CREATE TABLE pgss_row_pair_wide(k bigint)")
	for _, operator := range []string{"<", "<=", ">", ">="} {
		t.Run(operator, func(t *testing.T) {
			// Both operand lists contain genuine Vars, whose types are ignored.
			// Equal effective range names isolate pair provider identity rather
			// than selecting a different relation name or constant type.
			bind := func(table string) (analyze.StatementJumble, plan.RowComparisonExpr) {
				source := "SELECT (a.k,a.k) " + operator + " (a.k,a.k) FROM " + table + " a"
				ast, binding := pgssAnalyzedBinding(t, e, source)
				project, ok := binding.Query.Root.(*plan.ProjectNode)
				if !ok || len(project.Expressions) != 1 {
					t.Fatal("missing real row comparison projection")
				}
				row, ok := project.Expressions[0].(plan.RowComparisonExpr)
				if !ok || row.Operator != operator || len(row.Comparisons) != 2 {
					t.Fatal("missing actual two-pair row comparison")
				}
				return analyze.JumbleStatement(ast, binding), row
			}
			narrow, narrowRow := bind("pgss_row_pair_narrow")
			wide, wideRow := bind("pgss_row_pair_wide")
			for i, pair := range narrowRow.Comparisons {
				other := wideRow.Comparisons[i]
				// Builtin pair declarations live in Info; RoutineRef is reserved
				// for installed comparison authority, not builtin operator OIDs.
				routine := pair.Info.CallRoutineReference()
				otherRoutine := other.Info.CallRoutineReference()
				if routine == nil || otherRoutine == nil || routine.RoutineOID() == 0 || otherRoutine.RoutineOID() == 0 || routine.RoutineOID() == otherRoutine.RoutineOID() {
					t.Fatal("fixture did not select distinct genuine int4/int8 pair routines")
				}
				if pair.Predicate.Equal(other.Predicate) {
					t.Fatal("fixture did not bind distinct int4/int8 predicate providers")
				}
			}
			if narrow.QueryID() == 0 || narrow.QueryID() != wide.QueryID() {
				t.Fatal("ignored row pair provider or Var type entered identity")
			}
		})
	}
}
