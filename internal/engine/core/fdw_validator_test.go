package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Cancel on actual routine activation, independently of timing or Err call counts.
type foreignValidatorCancel struct {
	context.Context
	cancel context.CancelFunc
	scope *core.StatementScope
}

func (c foreignValidatorCancel) Err() error {
	if core.ScopeActiveSQLRoutineDepth(c.scope) != 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestFDWSQLValidatorInvocation(t *testing.T) {
	for _, outcome := range []string{"success", "error", "null", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, session, "CREATE TABLE unit_validation_log(v integer)")
			body := "INSERT INTO unit_validation_log VALUES(7); SELECT 7"
			if outcome == "error" {
				body = "INSERT INTO unit_validation_log VALUES(7); SELECT ($1[1])::integer"
			} else if outcome == "null" {
				body = "INSERT INTO unit_validation_log VALUES(7); SELECT NULL::integer"
			}
			enginetest.SessionSQL(t, session, "CREATE FUNCTION unit_validator(text[],oid) RETURNS integer STRICT LANGUAGE SQL AS $$"+body+"$$")
			read := e.CatalogRead()
			namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
			logAddress := read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "unit_validation_log"})
			var declaration *catalog.Procedure
			for _, address := range read.State().ProceduresNamed(namespace, "unit_validator") {
				p, ok := read.Procedure(address)
				if ok && p.Signature == catalog.ForeignValidatorSignature {
					declaration = p
				}
			}
			if declaration == nil {
				t.Fatal("missing real SQL validator")
			}
			op, err := session.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if !scope.Finished {
					scope.Finish(context.Canceled)
				}
			}()
			// Enter through ordinary description and execution so the scope owns
			// a real transaction reference, lock ticket and accepted snapshot.
			enclosing := enginetest.Statement(t, "SELECT 1")
			description, err := scope.Describe(enclosing)
			if err != nil {
				t.Fatal(err)
			}
			scope.Expected = &description
			result, err := scope.Execute(enclosing, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Rows == nil {
				t.Fatal("missing admitted enclosing result")
			}
			row := make([]sqltype.Value, len(result.Rows.Schema()))
			if ok, err := result.Rows.Next(op.Context(), row); err != nil || !ok {
				t.Fatal("enclosing first row", ok, err)
			}
			if ok, err := result.Rows.Next(op.Context(), row); err != nil || ok {
				t.Fatal("enclosing EOF", ok, err)
			}
			if err := result.Rows.Close(); err != nil {
				t.Fatal(err)
			}
			core.ScopeForeignUnlocked(scope)
			if outcome == "cancel" {
				ctx, cancel := context.WithCancel(op.Ctx)
				defer cancel()
				op.Ctx = foreignValidatorCancel{Context: ctx, cancel: cancel, scope: scope}
			}
			options := []catalog.ForeignOption{{Name: "a", Value: "one"}, {Name: "b", Value: "two=three"}, {Name: "empty", Value: ""}}
			frame, err := core.ForeignValidatorFrame(declaration.SQL.Inputs, catalog.ForeignServer, options)
			if err != nil {
				t.Fatal(err)
			}
			owned := frame.Parameters[0]
			options[0].Value = "mutated"
			if owned.IsNull() || owned.Type() != declaration.SQL.Inputs[0].Type || owned.ArrayLen() != 3 || owned.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: 3}) {
				t.Fatal("typed array shape", owned)
			}
			for i, want := range []string{"a=one", "b=two=three", "empty="} {
				got, err := owned.ArrayElement(i).Text()
				if err != nil || got != want {
					t.Fatal("owned ordered argument", i, got, err)
				}
			}
			class, err := frame.Parameters[1].OID()
			if err != nil || class != 1417 || frame.Inputs[1].Type.ID() != sqltype.Oid {
				t.Fatal("typed class argument", class, err)
			}
			before := e.Catalog.State
			err = core.ScopeInvokeForeignValidatorFrame(scope, declaration, frame)
			if outcome == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("routine cancellation", err)
				}
			} else if outcome == "error" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Context != `SQL function "unit_validator" statement 2` {
					t.Fatal("real SQL validator error", err)
				}
			} else if outcome == "null" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != fmt.Sprintf("function %d returned NULL", declaration.Address().ObjectID) || diagnostic.Context != "" {
					t.Fatal("NULL validator boundary", err)
				}
			} else if err != nil {
				t.Fatal("ignored non-void result", err)
			}
			if frame.Parameters != nil || core.ScopeActiveSQLRoutineDepth(scope) != 0 || e.Catalog.State != before {
				t.Fatal("validator retained frame/activation or published metadata")
			}
			primary := err
			if scope.Catalog == nil {
				t.Fatal("validator effects lack statement-private journal")
			}
			if outcome != "cancel" && len(scope.Stage.Rows[logAddress]) != 1 {
				t.Fatal("validator local writes were lost before publication")
			}
			text, err := owned.ArrayElement(0).Text()
			if err != nil || text != "a=one" {
				t.Fatal("frame cleanup mutated immutable array", text, err)
			}
			finish := scope.Finish(primary)
			if outcome == "success" && finish != nil {
				t.Fatal("validator effects publication", finish)
			}
			wantRows := 0
			if outcome == "success" {
				wantRows = 1
			}
			if got := len(e.Storage.Relations[logAddress].Versions); got != wantRows {
				t.Fatal("validator atomic effects", got, wantRows)
			}
		})
	}
	t.Run("empty_and_missing_implementation", func(t *testing.T) {
		e := engine.New()
		read := e.CatalogRead()
		text, _ := read.LookupType(sqltype.Text)
		array, ok := read.LookupArray(text)
		if !ok {
			t.Fatal("missing reciprocal text array descriptor")
		}
		oid, _ := read.LookupType(sqltype.Oid)
		frame, err := core.ForeignValidatorFrame([]sqltype.Usage{array.Usage(), oid.Usage()}, catalog.ForeignWrapper, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer frame.Close()
		if frame.Parameters[0].IsNull() || frame.Parameters[0].ArrayLen() != 0 || frame.Parameters[0].ArrayRank() != 0 {
			t.Fatal("empty options must be a non-NULL empty array")
		}
		class, err := frame.Parameters[1].OID()
		if err != nil || class != 2328 {
			t.Fatal("empty wrapper class", class, err)
		}
		// The installed language handler is a genuine catalog procedure, but
		// supplies neither a SQL body nor a registered foreign validator.
		language, err := catalog.LookupLanguage(read, "plpgsql")
		if err != nil {
			t.Fatal(err)
		}
		unavailable, ok := read.Procedure(language.Call)
		if !ok {
			t.Fatal("missing installed language handler")
		}
		wrapper := &catalog.ForeignObject{ObjectName: "unit_wrapper", Validator: unavailable.Address()}
		_, _, err = fdw.ValidatorJob(read, wrapper, catalog.ForeignWrapper, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" || diagnostic.Message != `foreign-data wrapper "unit_wrapper" has no registered validator` {
			t.Fatal("unavailable implementation was accepted", err)
		}
	})
}
