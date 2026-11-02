package enginetests

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

var _ expr.StatementJumbler = plan.GinEnumCompareExpr{}

func TestPgStatStatementsFingerprintBtreeGINCompound(t *testing.T) {
	e := engine.New()
	for _, source := range []string{
		"CREATE EXTENSION btree_gin VERSION '1.3'",
		"CREATE TYPE compound_jumble_enum AS ENUM('z','a','m')",
		"CREATE TYPE compound_jumble_other AS ENUM('z','a','m')",
		"CREATE SCHEMA compound_jumble_schema",
	} {
		enginetest.Run(t, e, source)
	}
	const source = "SELECT public.gin_enum_cmp('z'::compound_jumble_enum,'a'::compound_jumble_enum)"
	ast, binding := pgssAnalyzedBinding(t, e, source)
	project, ok := binding.Query.Root.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != 1 {
		t.Fatal("missing genuine comparator projection")
	}
	original, ok := project.Expressions[0].(plan.GinEnumCompareExpr)
	if !ok || original.RoutineReference == nil || original.Ref.Address.ObjectID == 0 {
		t.Fatal("lost selected comparator authority", project.Expressions[0])
	}
	before := analyze.JumbleStatement(ast, binding)
	if before.QueryID() == 0 {
		t.Fatal("zero comparator query identity")
	}
	normalized, raw := parser.NormalizeStatementText(source, 0, before.HighestParameter, before.Locations, before.Lists, before.UtilityLocations, parser.Options{StandardConformingStrings: true})
	if normalized != "SELECT public.gin_enum_cmp($1::compound_jumble_enum,$2::compound_jumble_enum)" || raw != source {
		t.Fatal("jumble lost ordered enum literal occurrences", normalized, raw)
	}
	equivalent := "SELECT /* 雪 */ public.gin_enum_cmp('m'::compound_jumble_enum,'z'::compound_jumble_enum) AS ignored"
	if got := pgssAnalyzedJumble(t, e, equivalent); got.QueryID() != before.QueryID() {
		t.Fatal("literal value, source position or output label changed comparator identity")
	}
	other := "SELECT public.gin_enum_cmp('z'::compound_jumble_other,'a'::compound_jumble_other)"
	if got := pgssAnalyzedJumble(t, e, other); got.QueryID() == before.QueryID() {
		t.Fatal("different concrete enum constants lost their type identity")
	}
	forward := "SELECT public.gin_enum_cmp(enum_first(NULL::compound_jumble_enum),enum_last(NULL::compound_jumble_enum))"
	reverse := "SELECT public.gin_enum_cmp(enum_last(NULL::compound_jumble_enum),enum_first(NULL::compound_jumble_enum))"
	if a, b := pgssAnalyzedJumble(t, e, forward), pgssAnalyzedJumble(t, e, reverse); a.QueryID() == b.QueryID() {
		t.Fatal("jumble ignored ordered callable operands")
	}
	rebuilt, err := expr.Substitute(original, expr.InfoOf(original), original.Children())
	if err != nil {
		t.Fatal(err)
	}
	rebound := rebuilt.(plan.GinEnumCompareExpr)
	if rebound.Ref != original.Ref || rebound.RoutineReference != original.RoutineReference {
		t.Fatal("reconstruction replaced installed comparator authority")
	}
	projectCopy := *project
	projectCopy.Expressions = []expr.Expr{rebuilt}
	queryCopy := *binding.Query
	queryCopy.Root = &projectCopy
	bindingCopy := *binding
	bindingCopy.Query = &queryCopy
	if got := analyze.JumbleStatement(ast, &bindingCopy); got.QueryID() != before.QueryID() {
		t.Fatal("reconstruction changed comparator jumble")
	}
	enginetest.Run(t, e, "ALTER FUNCTION public.gin_enum_cmp(anyenum,anyenum) RENAME TO compound_jumble_compare")
	enginetest.Run(t, e, "ALTER FUNCTION public.compound_jumble_compare(anyenum,anyenum) SET SCHEMA compound_jumble_schema")
	presented := "SELECT compound_jumble_schema.compound_jumble_compare('z'::compound_jumble_enum,'a'::compound_jumble_enum)"
	if got := pgssAnalyzedJumble(t, e, presented); got.QueryID() != before.QueryID() {
		t.Fatal("mutable function/schema presentation replaced selected callable identity")
	}
	// Reinstall the real recipe without replacing either enum. Thus the genuine
	// routine OID alone changes in an otherwise identical analyzed expression.
	enginetest.Run(t, e, "DROP EXTENSION btree_gin CASCADE")
	enginetest.Run(t, e, "CREATE EXTENSION btree_gin VERSION '1.3'")
	newAST, newBinding := pgssAnalyzedBinding(t, e, source)
	newProject, ok := newBinding.Query.Root.(*plan.ProjectNode)
	if !ok || len(newProject.Expressions) != 1 {
		t.Fatal("missing reinstalled comparator projection")
	}
	current, ok := newProject.Expressions[0].(plan.GinEnumCompareExpr)
	if !ok || current.Ref.Address == original.Ref.Address || current.Args[0].ResultUsage() != original.Args[0].ResultUsage() || current.Args[1].ResultUsage() != original.Args[1].ResultUsage() {
		t.Fatal("fixture did not isolate actual routine identity replacement")
	}
	if got := analyze.JumbleStatement(newAST, newBinding); got.QueryID() == before.QueryID() {
		t.Fatal("jumble omitted genuine selected routine OID")
	}
	if got := analyze.JumbleStatement(ast, binding); got.QueryID() != before.QueryID() {
		t.Fatal("later catalog changes mutated retained analyzed structure")
	}
}
