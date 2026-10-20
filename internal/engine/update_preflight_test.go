package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestUpdateCoreRecursivePreflight(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(i INT,n INT)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	ast := statement(t, "UPDATE t SET i=int4($1::text),n=i WHERE i=int4($1::text)")
	original := func() *boundUpdate {
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := e.resolveReference(auth, ast.(*parser.Update).Name)
		if err != nil {
			t.Fatal(err)
		}
		b, err := describe(op.ctx, e.analysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return b.Update
	}()
	cast := original.Assignments[0].(plan.CoercionExpr)
	column := original.Assignments[1]
	source := cast.Child.ResultUsage().Type
	for _, input := range []string{"7", "oops", "", "17", "oops", "42"} {
		value := sqltype.Null(source)
		var err error
		if input != "" {
			value, err = source.Input(context.Background(), input, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{value}}}
		private, err := preflightUpdate(&evaluation, original)
		if input == "oops" {
			state(t, err, "22P02")
			if private != nil {
				t.Fatal("failed preflight returned partially folded update")
			}
		} else {
			if err != nil || private == original {
				t.Fatal(private, err)
			}
			literal, ok := private.Assignments[0].(plan.LiteralExpr)
			if !ok || literal.Info != cast.Info {
				t.Fatal("fold lost conservative frame/source metadata", private.Assignments[0])
			}
			actual, err := literal.Eval(&evaluation)
			if err != nil {
				t.Fatal(err)
			}
			if input == "" {
				if !actual.IsNull() {
					t.Fatal(actual)
				}
			} else {
				n, err := actual.Integer()
				if err != nil {
					t.Fatal(err)
				}
				want := int64(7)
				if input == "17" {
					want = 17
				}
				if input == "42" {
					want = 42
				}
				if n != want {
					t.Fatal("stale imported frame", n, want)
				}
			}
			if !reflect.DeepEqual(private.Assignments[1], column) {
				t.Fatal("unchanged row-dependent branch replaced")
			}
			if &private.Assignments[0] == &original.Assignments[0] {
				t.Fatal("assignment backing storage shared after fold")
			}
		}
		if _, ok := original.Assignments[0].(plan.CoercionExpr); !ok {
			t.Fatal("analyzed assignment replaced")
		}
		if _, ok := original.Predicate.(plan.ComparisonExpr).Right.(plan.CoercionExpr); !ok {
			t.Fatal("analyzed predicate replaced")
		}
	}
	// A harmless assignment cannot mask a missing WHERE preflight traversal.
	whereOnly := *original
	whereOnly.Assignments = []expr.Expr{column}
	bad, err := source.Input(context.Background(), "oops", -1)
	if err != nil {
		t.Fatal(err)
	}
	whereEvaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{bad}}}
	failed, err := preflightUpdate(&whereEvaluation, &whereOnly)
	state(t, err, "22P02")
	if failed != nil {
		t.Fatal("failed WHERE preflight returned a partially prepared update")
	}
	unchanged := &boundUpdate{Assignments: []expr.Expr{column}}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	same, err := preflightUpdate(&evaluation, unchanged)
	if err != nil || same != unchanged {
		t.Fatal("unchanged update allocated/replaced", same, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	evaluation.Context = canceled
	private, err := preflightUpdate(&evaluation, original)
	if !errors.Is(err, context.Canceled) || private != nil {
		t.Fatal(private, err)
	}
}

// Reuse the existing test-owned preflight context: no production hook or sleep.
func TestUpdateCoreRecursivePreflightAuthorityIdentityContext(t *testing.T) {
	for _, mode := range []string{"authority", "storage", "attribute", "type", "width", "comment", "acl", "epoch", "preflight", "relock", "deadline", "teardown", "success"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(i INT)")
			run(t, e, "INSERT INTO t VALUES(7)")
			run(t, e, "CREATE ROLE actor LOGIN")
			actor, _ := e.Roles().Lookup("actor")
			int8Usage := testUsage(e, sqltype.Int8)
			ctx := &castPreflightContext{Context: context.Background(), e: e, t: t, cancelAt: mode}
			op, done, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			ctx.Context = op.ctx
			op.ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			// Release admission even if a diagnostic assertion calls Fatal.
			defer scope.Finish(context.Canceled)
			deadline, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
			defer cancel()
			var restore func()
			ctx.visit = func() {
				if scope.view != nil {
					t.Error("UPDATE captured source before preflight/current ACL")
				}
				if scope.lockedRef.Domain() != e.catalog.Domain || scope.lockedDescriptor == nil {
					t.Error("lost exact locked relation")
				}
				switch mode {
				case "authority":
					op.session.mu.Lock()
					op.session.authorization.state.CurrentUser = actor.Ref
					op.session.mu.Unlock()
				case "storage":
					address := scope.lockedRef.Address
					original := e.storage.Relations[address]
					replacement := *original
					e.storage.InstallRelationLocked(address, &replacement)
					restore = func() { e.storage.InstallRelationLocked(address, original) }
				case "attribute", "type", "width", "comment", "acl":
					address := scope.lockedRef.Address
					original := e.catalog.state.Get(address)
					replacement := *scope.lockedDescriptor.(*catalog.Relation)
					replacement.ObjectRevision++
					switch mode {
					case "attribute":
						replacement.Attributes = append([]int32(nil), replacement.Attributes...)
						replacement.Attributes[0]++
					case "type":
						replacement.Columns = append([]catalog.Column(nil), replacement.Columns...)
						replacement.Columns[0].Usage = int8Usage
					case "width":
						replacement.Columns = nil
						replacement.Attributes = nil
					case "comment":
						replacement.Comment = "compatible metadata revision"
					case "acl":
						replacement.Security.ACL = authorization.ACLValue{Explicit: true}
					}
					e.catalog.state.SetDescriptor(address, &replacement)
					restore = func() { e.catalog.state.SetDescriptor(address, original) }
				case "epoch":
					original := e.catalog.state.Epoch()
					e.catalog.state.SetEpoch(e.catalog.state.Epoch() + 1)
					restore = func() { e.catalog.state.SetEpoch(original) }
				case "deadline":
					ctx.Context = deadline
				case "teardown":
					op.session.RequestEnd(CleanClose)
				}
			}
			result, executeErr := scope.Execute(statement(t, "UPDATE t SET i=int4($1::text) WHERE i IS NOT NULL"), 1, func(c context.Context, signature ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
				value, err := signature.Types[0].Type.Input(c, "42", -1)
				if err != nil {
					return err
				}
				frame[0] = value
				ctx.armed = true
				return nil
			})
			ctx.armed = false
			if restore != nil && mode != "comment" && mode != "acl" {
				e.mu.Lock()
				restore()
				e.mu.Unlock()
			}
			if !ctx.entered {
				t.Error("preflight never entered")
			}
			switch mode {
			case "authority":
				state(t, executeErr, "42501")
			case "storage", "attribute", "type", "width", "epoch":
				if !errors.Is(executeErr, ids.ErrCatalogReplaced) {
					t.Error(executeErr)
				}
			case "preflight", "relock", "teardown":
				if !errors.Is(executeErr, context.Canceled) {
					t.Error(executeErr)
				}
			case "deadline":
				if !errors.Is(executeErr, context.DeadlineExceeded) {
					t.Error(executeErr)
				}
			case "success", "comment", "acl":
				if executeErr != nil || result.Count != 1 || result.Rows != nil {
					t.Error(result, executeErr)
				}
			}
			if mode != "success" && mode != "comment" && mode != "acl" && (result.Count != 0 || result.Rows != nil || scope.view != nil) {
				t.Error("failure acquired source/result", result)
			}
			address := scope.lockedRef.Address
			if err := scope.Finish(executeErr); err != executeErr {
				t.Error("Finish changed failure", err, executeErr)
			}
			if mode == "comment" || mode == "acl" {
				store := e.storage.Relations[address]
				if store == nil || len(store.Versions) != 1 || testExport(store.Versions[0].Values[0]) != int64(42) {
					t.Error("compatible metadata lost publication")
				}
			}
			lockEmpty(t, e)
			if scope.view != nil || scope.lockedDescriptor != nil || scope.ticket != nil || !scope.finished {
				t.Error("statement pins/holds survived Finish")
			}
		})
	}
}
