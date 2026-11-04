package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func sepgsqlCodecEngine(t *testing.T, inputLabel string) (*core.Engine, *sqltype.Descriptor) {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION hstore")
	runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{
		ID: "actual-codec-owner", Version: "1", Contexts: []string{"A", "R", "T", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "U",
		DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_sequence * P\ndb_view * P\ndb_procedure * P\n",
		Rules: []sepgsql.Rule{
			{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: "R", Target: "P", Class: "db_database", Allow: []string{"access"}},
			{Source: "R", Target: "P", Class: "db_schema", Allow: []string{"search"}},
			{Source: "R", Target: "P", Class: "db_procedure", Allow: []string{"execute"}},
			{Source: "R", Target: "P", Class: "db_table", Allow: []string{"select", "insert"}},
			{Source: "R", Target: "P", Class: "db_column", Allow: []string{"select", "insert"}},
			{Source: "R", Target: "S", Class: "db_procedure", Allow: []string{"entrypoint"}, Deny: []string{"execute"}},
			{Source: "R", Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition", "transition"}},
		}, TrustedTransitions: []sepgsql.TrustedTransition{{Source: "R", Procedure: "S", Result: "T"}, {Source: "R", Procedure: "U", Result: "T"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(runtime, true)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION hstore_in(cstring) IS '"+inputLabel+"'")
	enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION hstore_out(hstore) IS 'S'")
	read := e.CatalogRead()
	address, ok := read.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "hstore"})
	if !ok {
		t.Fatal("missing installed hstore type")
	}
	typ, ok := read.Type(address)
	if !ok {
		t.Fatal("missing canonical hstore descriptor")
	}
	return e, typ.SQLType
}

func TestSepgsqlSimulationCodecs(t *testing.T) {
	t.Run("actual_kernel_unwind_and_original_generation", func(t *testing.T) {
		e, typ := sepgsqlCodecEngine(t, "S")
		_, foreignType := sepgsqlCodecEngine(t, "S")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		t.Cleanup(done)
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		finished := false
		t.Cleanup(func() {
			if !finished {
				scope.Finish(nil)
			}
		})
		if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := scope.RoutineAuthorizationLocked()
		if err == nil {
			err = scope.CaptureIdentityLocked(auth)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		read := auth.Catalog
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: read}}
		value, err := typ.InputWithEnvironment(op.Context(), "k=>v", -1, evaluation.FunctionInput())
		if err != nil || value.IsNull() || value.Type() != typ {
			t.Fatal("actual input kernel", value, err)
		}
		text, err := typ.OutputWithEnvironment(op.Context(), value, evaluation.FunctionOutput())
		if err != nil || text != `"k"=>"v"` {
			t.Fatal("actual output kernel", text, err)
		}
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("successful codec leaked trusted subject", op.Session.Sepgsql.Current())
		}
		if _, err = typ.InputWithEnvironment(op.Context(), "broken", -1, evaluation.FunctionInput()); err == nil {
			t.Fatal("invalid input did not reach genuine parser error")
		}
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("parser error leaked trusted subject")
		}
		if _, err = evaluation.EnterTypeIO(op.Context(), foreignType, sqltype.TypeIOInput); err == nil {
			t.Fatal("same numeric foreign type adopted codec authority")
		}
		missing := evaluation
		missing.Host = nil
		if _, err = typ.InputWithEnvironment(op.Context(), "k=>v", -1, missing.FunctionInput()); err == nil {
			t.Fatal("hostless active codec gained authority")
		}
		closed := evaluation
		closed.ClosedEvaluation = &exec.EvaluationCapture{}
		if _, err = typ.InputWithEnvironment(op.Context(), "k=>v", -1, closed.FunctionInput()); err == nil {
			t.Fatal("closed codec silently borrowed live host")
		}
		canceled, cancel := context.WithCancel(op.Context())
		cancel()
		if _, err = evaluation.EnterTypeIO(canceled, typ, sqltype.TypeIOInput); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled codec callback", err)
		}
		if _, err = catalog.CaptureTypeIO(read, typ, sqltype.TypeIOReceive); err == nil {
			t.Fatal("unimplemented binary direction acquired a fake callback")
		}
		enginetest.Run(t, e, "ALTER FUNCTION hstore_in(cstring) RENAME TO sepgsql_stale_hstore_in")
		if _, err = typ.InputWithEnvironment(op.Context(), "k=>v", -1, evaluation.FunctionInput()); err == nil {
			t.Fatal("stale selected helper silently adopted current descriptor")
		}
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("stale codec entry leaked trusted subject")
		}
		scope.Finish(nil)
		finished = true
		if _, err = typ.InputWithEnvironment(op.Context(), "k=>v", -1, evaluation.FunctionInput()); err == nil {
			t.Fatal("ended scope retained live codec authority")
		}
	})
	t.Run("entry_denial_precedes_real_parser", func(t *testing.T) {
		e, typ := sepgsqlCodecEngine(t, "U")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		t.Cleanup(done)
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { scope.Finish(nil) })
		if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := scope.RoutineAuthorizationLocked()
		if err == nil {
			err = scope.CaptureIdentityLocked(auth)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog}}
		_, err = typ.InputWithEnvironment(op.Context(), "broken", -1, evaluation.FunctionInput())
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "SELinux: security policy violation"})
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("denied input changed subject")
		}
	})
}
