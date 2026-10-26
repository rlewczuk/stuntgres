package core_test

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/parser"
)

func infoTriggersWhen(t *testing.T, e *core.Engine, scope *core.StatementScope, read catalog.Read, relation *catalog.Relation, definition stored.Trigger) stored.Trigger {
	t.Helper()
	syntax, err := parser.Parse("SELECT NEW.v>0")
	if err != nil {
		t.Fatal(err)
	}
	raw := syntax[0].(*parser.Select).Targets[0]
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	var condition expr.Expr
	var query *parser.Select
	var binding analyze.Binding
	if err == nil {
		condition, query, binding, err = (viewcmds.ViewHost{tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op}}}).BindTriggerWhen(auth, &analyze.RuleNamespace{Relation: relation, Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Event: "UPDATE", Trigger: true}, raw, scope.Input)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	p := definition.Parts()
	p.When = condition
	p.Binding = &binding
	p.Presentation, err = stored.FreezeQueryPresentation(read, ids.SessionToken{}, query, binding, false, false, 0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := stored.ExpressionDependRefs(scope.Op.Context(), read, condition)
	if err != nil {
		t.Fatal(err)
	}
	p.References = append(p.References, refs...)
	definition, err = stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}
