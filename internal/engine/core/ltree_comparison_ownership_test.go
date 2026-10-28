package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// These are ownership assertions on actual protocol definitions and accepted
// bindings, not another SQL-result fixture. Public outcomes live in reference/.
func TestLtreeComparisonPreparedOwnership(t *testing.T) {
	for _, tc := range []struct {
        name, expression, want string
        member, operator int
        result sqltype.ID
    }{
        {"equal", "'a.b'::ltree = 'a.b'::ltree", "t", extension.LtreeComparisonProcedureStart, extension.LtreeComparisonOperatorStart, sqltype.Bool},
        {"boundary_order", "'a.b'::ltree < 'a-b'::ltree", "t", extension.LtreeComparisonProcedureStart+2, extension.LtreeComparisonOperatorStart+2, sqltype.Bool},
    } {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE EXTENSION ltree")
			view := e.CatalogRead()
			installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
			procedure := view.MustProcedure(installation.Members[tc.member])
			query := "SELECT COALESCE((" + tc.expression + "),false) AS v"
			enginetest.SessionSQL(t, s, "BEGIN")
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
			registry := core.PayloadOf(s).PreparedSQL
			// The shared comparison's ordinary preflight keeps its executable
			// graph. A demanded COALESCE below reaches the existing conditional
			// fold boundary; do not change the configured prewalk to force it.
			command(func(scope *core.StatementScope) error {
				_,err:=scope.ParseProtocol("lt_plain","SELECT "+tc.expression+" AS v",nil);return err
			})
			command(func(scope *core.StatementScope) error {
				_,err:=scope.BindProtocolPortal("lt_plain_portal","lt_plain",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}});return err
			})
			plain:=registry.Binding("lt_plain_portal")
			if plain==nil||plain.Resource==nil||plain.Resource.Root==nil||len(plain.Resource.Root.Expressions)!=1{t.Fatal("missing actual plain comparison binding")}
			plainRoot:=plain.Resource.Root
			selected,ok:=plainRoot.Expressions[0].(plan.SelectedOperatorExpr)
			if !ok||selected.Operator==nil||selected.Ref.Address!=installation.Members[tc.operator]||selected.Operator.Function!=procedure.Address().ObjectID{t.Fatal("missing selected operator authority")}
			comparison,ok:=selected.Provider.(plan.ComparisonExpr)
			procedureRef:=view.Owner().Domain.Ref(view.Epoch(),procedure.Address())
			if !ok||comparison.RoutineReference!=procedure||comparison.RoutineRef!=procedureRef{t.Fatal("missing genuine shared comparison capture")}
			operands:=func(node plan.ComparisonExpr)[2]sqltype.Value{
				t.Helper();var values [2]sqltype.Value;count:=0
				plan.WalkExpression(node,func(child expr.Expr){
					if literal,ok:=child.(plan.LiteralExpr);ok&&literal.Value.Type()==view.MustType(installation.Members[3]).SQLType{
						if count==len(values){t.Fatal("comparison gained an operand")}
						values[count]=literal.Value;count++
					}
				})
				if count!=len(values){t.Fatal("comparison lost actual typed operand backing")}
				return values
			}
			originalOperands:=operands(comparison)
			assertPlain:=func(){
				t.Helper()
				if plain.Resource==nil||plain.Resource.Root!=plainRoot||len(plainRoot.Expressions)!=1{t.Fatal("accepted plain graph replaced")}
				current,ok:=plainRoot.Expressions[0].(plan.SelectedOperatorExpr)
				if !ok||current.Operator!=selected.Operator||current.Ref!=selected.Ref{t.Fatal("accepted plain operator capture replaced")}
				provider,ok:=current.Provider.(plan.ComparisonExpr)
				if !ok||provider.RoutineReference!=procedure||provider.RoutineRef!=procedureRef||provider.Operator!=comparison.Operator||provider.Source!=comparison.Source||provider.Occurrence!=comparison.Occurrence||operands(provider)!=originalOperands{t.Fatal("accepted comparison generation/declaration/operand backing changed")}
			}
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("lt_owned", query, nil)
				return err
			})
			definition := registry.Lookup("lt_owned")
			if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
				t.Fatal("missing real definition owner")
			}
			owner := definition.Description.Folds
			if len(owner.Current().Entries) != 0 || owner.Current().Generation != 0 {
				t.Fatal("Parse published evaluated values")
			}
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal(name, "lt_owned", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
			}
			bind("lt_first")
			first := registry.Binding("lt_first")
			accepted := owner.Current()
			if first == nil || first.Folds == nil || first.Entry.Description.Folds != owner || len(accepted.Entries) == 0 || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
				t.Fatal("binding lost accepted definition ownership")
			}
			index := -1
			for i, entry := range accepted.Entries {
				if entry.Result.Type.ID() != tc.result {
					continue
				}
				if entry.Callable != procedure || entry.Operator != procedureRef {
					continue
				}
				index = i
				break
			}
			if index < 0 {
				t.Fatal("conversion has no selected backing-procedure fold capture")
			}
			retained := accepted.Entries[index]
			value := retained.Value
			base:=view.MustType(installation.Members[3]).SQLType
			if len(retained.Inputs)!=2{t.Fatal("comparison lost ordered operand captures")}
			leftInput,rightInput:=retained.Inputs[0],retained.Inputs[1]
			for _,input:=range []exec.FoldInput{leftInput,rightInput}{
				if input.Usage.Type!=base||input.Value.Type()!=base||input.Value.IsNull(){t.Fatal("comparison lost genuine immutable operand generation")}
			}
			assertInputs:=func(entry exec.FoldConstant){
				t.Helper()
				if len(entry.Inputs)!=2||&entry.Inputs[0]!=&retained.Inputs[0]||entry.Inputs[0]!=leftInput||entry.Inputs[1]!=rightInput||entry.Callable!=procedure{t.Fatal("accepted operand backing or original declaration replaced")}
			}
			if value.Type() != retained.Result.Type || value.Type() != definition.Description.Results[0].Usage.Type {
				t.Fatal("retained datum lost shape or exact descriptor ownership")
			}
			output, err := value.Type().Output(context.Background(), value)
			if err != nil || output != tc.want {
				t.Fatal("retained typed payload", output, err)
			}
			assertGraph := func(binding *core.PreparedBinding) {
				t.Helper()
				if binding.Resource==nil||binding.Resource.Root==nil||len(binding.Resource.Root.Expressions)!=1{t.Fatal("accepted binding lost its private query graph")}
				found:=false
				plan.WalkExpression(binding.Resource.Root.Expressions[0],func(node expr.Expr){
					wrapper,ok:=node.(plan.SelectedOperatorExpr);if !ok||wrapper.Ref!=selected.Ref{return}
					if found||wrapper.Operator!=selected.Operator||wrapper.RoutineReference!=nil{t.Fatal("accepted graph lost exact selected wrapper")}
					found=true
					literal,ok:=wrapper.Provider.(plan.LiteralExpr)
					if !ok||literal.Value!=value||literal.ResultUsage()!=retained.Result||literal.Source!=retained.Source||literal.Occurrence!=retained.Occurrence||literal.RoutineReference!=nil{t.Fatal("accepted graph lost immutable comparison fold/provenance")}
				})
				if !found{t.Fatal("accepted conditional graph lost selected comparison")}
				operatorDependency,backingDependency:=false,false
				for _,dependency:=range binding.Entry.Description.Analysis.Dependencies{
					operatorDependency=operatorDependency||dependency.Ref==selected.Ref&&dependency.Revision==selected.Operator.Revision()
					backingDependency=backingDependency||dependency.Ref==procedureRef&&dependency.Revision==procedure.Revision()
				}
				if !operatorDependency||!backingDependency{t.Fatal("analysis lost exact operator/backer dependencies")}
				assertPlain()
			}
			assertGraph(first)
			firstRoot := first.Resource.Root
			bind("lt_second")
			second := registry.Binding("lt_second")
			current := owner.Current()
			if second == nil || second == first || second.Entry.Description.Folds != owner || second.Folds.Generation != accepted.Generation || current.Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] || &current.Entries[0] != &accepted.Entries[0] || current.Entries[index].Value != value || !samePublishedFold(retained, current.Entries[index]) {
				t.Fatal("repeated Bind replaced immutable backing or capture provenance")
			}
			assertInputs(current.Entries[index])
			assertInputs(first.Folds.Entries[index])
			assertInputs(second.Folds.Entries[index])
			assertGraph(second)
			assertGraph(first)
			secondRoot := second.Resource.Root
			if secondRoot == firstRoot {
				t.Fatal("distinct accepted bindings shared a mutable resource graph")
			}

			for _, cause := range []string{"cancellation", "retirement", "generation"} {
				t.Run(cause, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					op, err := s.Acquire(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer op.Done()
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					local := cloneFoldDescription(t, definition.Description)
					before := snapshotFoldDescription(local)
					scope.Expected = &local
					e.Mu.Lock()
					auth, err := e.AuthorizationLocked(op)
					if err != nil {
						e.Mu.Unlock()
						scope.Finish(err)
						t.Fatal(err)
					}
					environment, err := e.PreparedEnvironmentLocked(auth, true)
					if err != nil {
						e.Mu.Unlock()
						scope.Finish(err)
						t.Fatal(err)
					}
					publication, err := core.BeginFoldPublication(&local, definition.Body, environment, auth.Catalog, nil)
					if err != nil || publication == nil {
						e.Mu.Unlock()
						scope.Finish(err)
						t.Fatal("missing actual publication attempt", err)
					}
					publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
					publication.Group.Private = true
					publication.Group.Entries[index].Value = sqltype.Null(retained.Result.Type)
					switch cause {
					case "cancellation":
						cancel()
					case "retirement":
						local.Retire()
					case "generation":
						local.Folds.Invalidate(true)
					}
					fenced := local.Folds.Current()
					err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
					e.Mu.Unlock()
					_ = scope.Finish(err)
					if cause == "cancellation" && !errors.Is(err, context.Canceled) || cause != "cancellation" && !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed {
						t.Fatal("publication fence accepted private replacement", cause, err)
					}
					rejected := local.Folds.Current()
					if rejected.Generation != fenced.Generation || &rejected.Entries[0] != &before.Accepted.Entries[0] || rejected.Entries[index].Value != value || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] || first.Folds.Generation != accepted.Generation || second.Folds.Generation != accepted.Generation || first.Folds.Entries[index].Value != value || second.Folds.Entries[index].Value != value {
						t.Fatal("rejected publication changed accepted owner, generation or immutable backing")
					}
					assertInputs(owner.Current().Entries[index])
					assertInputs(first.Folds.Entries[index])
					assertInputs(second.Folds.Entries[index])
					if first.Resource.Root != firstRoot || second.Resource.Root != secondRoot {
						t.Fatal("rejected publication replaced an accepted portal graph")
					}
					assertGraph(first)
					assertGraph(second)
				})
			}
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("lt_owned") })
			if owner.Live() || first.Retired || second.Retired || first.Folds.Entries[index].Value != value || second.Folds.Entries[index].Value != value {
				t.Fatal("definition retirement mutated accepted portals")
			}
			assertGraph(first)
			assertGraph(second)
			for _, name := range []string{"lt_first", "lt_second"} {
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
			}
			if !first.Retired || !second.Retired {
				t.Fatal("portal retirement leaked ownership")
			}
			assertPlain()
			command(func(scope *core.StatementScope)error{return scope.CloseProtocolStatement("lt_plain")})
			command(func(scope *core.StatementScope)error{return scope.CloseProtocolPortal("lt_plain_portal")})
			op, _, err := s.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			err = e.SyncProtocolCycle(op, cycle)
			op.Done()
			if err != nil {
				t.Fatal(err)
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
