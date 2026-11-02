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

func TestPgsurgeryFreezeProviderStatementJumble(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pg_surgery")
	done()
	source := "SELECT heap_force_freeze(tids => '{}'::tid[], reloid => '1'::regclass)"
	ast, binding := pgssAnalyzedBinding(t, e, source)
	project, ok := binding.Query.Root.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != 1 {
		t.Fatal("missing actual bound scalar projection")
	}
	call, ok := project.Expressions[0].(plan.InstalledPgsurgeryExpr)
	if !ok || call.Procedure == nil || call.CallRoutineReference() != call.Procedure || call.Ref.Address != call.Procedure.Address() || call.Slots != ([2]uint8{1, 0}) {
		t.Fatal("bound jumble lost installed identity/formal projection")
	}
	if _, ok := any(call).(expr.StatementJumbler); !ok {
		t.Fatal("installed wrapper lacks its own discriminator")
	}
	jumble := func(node expr.Expr) analyze.StatementJumble {
		projectCopy := *project
		projectCopy.Expressions = []expr.Expr{node}
		queryCopy := *binding.Query
		queryCopy.Root = &projectCopy
		bindingCopy := *binding
		bindingCopy.Query = &queryCopy
		return analyze.JumbleStatement(ast, &bindingCopy)
	}
	before := jumble(call)
	rebuilt, err := call.Rebuild(expr.InfoOf(call), call.Children())
	if err != nil {
		t.Fatal(err)
	}
	copy := rebuilt.(plan.InstalledPgsurgeryExpr)
	if copy.Ref != call.Ref || copy.Procedure != call.Procedure || copy.Slots != call.Slots || &copy.Args[0] == &call.Args[0] || jumble(copy).QueryID() != before.QueryID() {
		t.Fatal("reconstruction changed selected identity or borrowed children")
	}
	if before.QueryID() == jumble(call.Pgsurgery).QueryID() {
		t.Fatal("installed wrapper borrowed kernel discriminator")
	}
	changedSlots := call
	changedSlots.Slots = [2]uint8{0, 1}
	if before.QueryID() == jumble(changedSlots).QueryID() {
		t.Fatal("formal projection was omitted from statement identity")
	}
	changedChild := call
	changedChild.Args = append([]expr.Expr(nil), call.Args...)
	changedChild.Args[0] = call.Args[1]
	if before.QueryID() == jumble(changedChild).QueryID() {
		t.Fatal("lexical operand identity was omitted")
	}
	other := pgssAnalyzedJumble(t, e, "SELECT heap_force_freeze(tids => '{\"(0,1)\"}'::tid[], reloid => '2'::regclass)")
	if before.QueryID() == 0 || before.QueryID() != other.QueryID() {
		t.Fatal("literal payload changed selected installed identity")
	}
	enginetest.Run(t, e, "ALTER FUNCTION heap_force_freeze(regclass,tid[]) RENAME TO genuine_freeze")
	renamed := pgssAnalyzedJumble(t, e, "SELECT genuine_freeze(tids => '{}'::tid[], reloid => '1'::regclass)")
	if before.QueryID() != renamed.QueryID() {
		t.Fatal("mutable spelling replaced genuine routine identity")
	}
	enginetest.Run(t, e, "CREATE FUNCTION heap_force_freeze(reloid regclass,tids tid[]) RETURNS void LANGUAGE SQL AS 'SELECT NULL::void'")
	ordinary := pgssAnalyzedJumble(t, e, source)
	if before.QueryID() == ordinary.QueryID() {
		t.Fatal("ordinary lookalike borrowed installed statement identity")
	}
	for _, value := range []analyze.StatementJumble{before, jumble(copy)} {
		normalized, raw := parser.NormalizeStatementText(source, 0, value.HighestParameter, value.Locations, value.Lists, value.UtilityLocations, parser.Options{StandardConformingStrings: true})
		if normalized != "SELECT heap_force_freeze(tids => $1::tid[], reloid => $2::regclass)" || raw != source {
			t.Fatal("jumble lost lexical literal locations", normalized, raw)
		}
	}
}
