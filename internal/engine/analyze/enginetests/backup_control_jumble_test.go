package enginetests

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

var _ expr.StatementJumbler = functions.BackupControl{}

// Exercise the pre-fold visitor, not Eval: even strict NULL and demand-free
// calls must be classified before execution or scalar NULL simplification.
func TestBackupControlStatementJumble(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		source     string
		normalized string
		oid        uint32
	}{
		{"SELECT pg_backup_start(NULL)", "SELECT pg_backup_start($1)", 2172},
		{"SELECT pg_backup_start('label')", "SELECT pg_backup_start($1)", 2172},
		{"SELECT pg_backup_start(fast=>NULL::boolean,label=>'label')", "SELECT pg_backup_start(fast=>$1::boolean,label=>$2)", 2172},
		{"SELECT pg_backup_start($1::text,$2::boolean)", "SELECT pg_backup_start($1::text,$2::boolean)", 2172},
		{"SELECT pg_backup_stop()", "SELECT pg_backup_stop()", 2739},
		{"SELECT pg_backup_stop(NULL)", "SELECT pg_backup_stop($1)", 2739},
		{"SELECT pg_backup_stop(wait_for_archive=>NULL)", "SELECT pg_backup_stop(wait_for_archive=>$1)", 2739},
		{"SELECT pg_backup_stop($1::boolean)", "SELECT pg_backup_stop($1::boolean)", 2739},
	} {
		t.Run(tc.source, func(t *testing.T) {
			ast, binding := pgssAnalyzedBinding(t, e, tc.source)
			project, ok := binding.Query.Root.(*plan.ProjectNode)
			if !ok || len(project.Expressions) != 1 {
				t.Fatal("missing backup call projection")
			}
			call, ok := project.Expressions[0].(functions.BackupControl)
			if !ok || call.CallRoutineReference() == nil || uint32(call.CallRoutineReference().RoutineOID()) != tc.oid {
				t.Fatal("missing backup recipe or selected routine", project.Expressions[0])
			}
			beforeCall := call
			fingerprint := func(node expr.Expr) analyze.StatementJumble {
				projectCopy := *project
				projectCopy.Expressions = []expr.Expr{node}
				queryCopy := *binding.Query
				queryCopy.Root = &projectCopy
				bindingCopy := *binding
				bindingCopy.Query = &queryCopy
				return analyze.JumbleStatement(ast, &bindingCopy)
			}
			before := fingerprint(call)
			for _, reconstruct := range []func(expr.Info, []expr.Expr) (expr.Expr, error){call.Rebuild, call.Substitute} {
				rebuilt, err := reconstruct(expr.InfoOf(call), call.Children())
				if err != nil {
					t.Fatal(err)
				}
				after := fingerprint(rebuilt)
				if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(call, beforeCall) {
					t.Fatal("reconstruction changed query identity, locations or original recipe", before, after)
				}
			}
			// The concrete tag must not replace selected routine identity. Keep
			// the genuine recipe/children while isolating the metadata projection.
			withoutRoutine := call
			withoutRoutine.RoutineReference = nil
			if fingerprint(withoutRoutine).QueryID() == before.QueryID() {
				t.Fatal("backup jumble discarded the selected routine OID")
			}
			normalized, raw := parser.NormalizeStatementText(tc.source, 0, before.HighestParameter, before.Locations, before.Lists, before.UtilityLocations, parser.Options{StandardConformingStrings: true})
			if normalized != tc.normalized || raw != tc.source {
				t.Fatalf("normalized=%q raw=%q; want %q and %q", normalized, raw, tc.normalized, tc.source)
			}
		})
	}
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"SELECT pg_backup_start('first')", "SELECT pg_backup_start('second')", true},
		{"SELECT pg_backup_start(NULL)", "SELECT pg_backup_start('second')", true},
		{"SELECT pg_backup_stop(NULL)", "SELECT pg_backup_stop(wait_for_archive=>NULL)", true},
		{"SELECT pg_backup_start('label')", "SELECT pg_backup_start('label',NULL)", false},
		{"SELECT pg_backup_stop()", "SELECT pg_backup_stop(NULL)", false},
		{"SELECT pg_backup_stop(NULL)", "SELECT pg_backup_stop($1::boolean)", false},
		{"SELECT pg_backup_stop($1::boolean) WHERE $2::boolean", "SELECT pg_backup_stop($2::boolean) WHERE $1::boolean", false},
		{"SELECT pg_backup_start('label')", "SELECT pg_backup_start(upper('label'))", false},
		{"SELECT pg_backup_start(upper('label'))", "SELECT pg_backup_start(lower('label'))", false},
	} {
		left := pgssAnalyzedJumble(t, e, tc.left)
		right := pgssAnalyzedJumble(t, e, tc.right)
		if same := left.QueryID() == right.QueryID(); same != tc.same {
			t.Fatalf("identity equality=%v, want %v: %s / %s", same, tc.same, tc.left, tc.right)
		}
	}
}
