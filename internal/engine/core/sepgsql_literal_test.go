package core_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/stats"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func sepgsqlLiteralEngine(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION hstore")
	runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{ID: "literal-owner", Version: "1", Contexts: []string{"A", "R", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "U", DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_sequence * P\ndb_view * P\ndb_procedure * P\n", Rules: []sepgsql.Rule{{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}}, {Source: "R", Target: "P", Class: "db_database", Allow: []string{"access"}}, {Source: "R", Target: "P", Class: "db_schema", Allow: []string{"search"}}, {Source: "R", Target: "P", Class: "db_procedure", Allow: []string{"execute"}}, {Source: "R", Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition", "transition"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(runtime, true)
	if err == nil {
		e.InitializeSepgsqlSeedLabels()
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION hstore(text,text) IS 'S'")
	return e
}

func sepgsqlLiteralBinding(t *testing.T, e *core.Engine) (expr.Expr, *exec.Context, stats.SnapshotHost, sqltype.Usage) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	read := e.Catalog.Current()
	codec := core.StatisticsSnapshotCodec(context.Background(), e, read)
	host, ok := codec.(stats.SnapshotHost)
	if !ok {
		t.Fatal("missing genuine codec host")
	}
	address, ok := read.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "hstore"})
	if !ok {
		t.Fatal("missing genuine hstore type")
	}
	typ, ok := read.Type(address)
	if !ok {
		t.Fatal("missing installed hstore descriptor")
	}
	usage := typ.SQLType.Usage()
	auth := host.Authority()
	binder := analyze.NewBinder(context.Background(), host.BindingHost(context.Background()), sqltype.InputEnvironment{}, analyze.Expected{})
	binder.Query.Auth = auth
	binder.Query.Namespace = &analyze.QueryNamespace{}
	binder.Params.NoParameters = true
	raw := enginetest.Statement(t, "SELECT hstore('k','v')").(*parser.Select).Targets[0]
	node, err := binder.BindDefault(raw, catalog.Column{Name: "private literal", Usage: usage})
	if err != nil {
		t.Fatal(err)
	}
	if err = binder.CaptureRetainedWriteInput([]expr.Expr{node}, nil, nil); err != nil {
		t.Fatal(err)
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: read, Input: binder.Input}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: auth, RoutineRecipe: true, RoutineChecks: binder.Dependencies, ControlLiteralAuthority: host.ControlLiteralAuthority()}}
	evaluation.Input.DomainChecks = evaluation
	return node, evaluation, host, usage
}

func TestSepgsqlSimulationRoutine(t *testing.T) {
	e := sepgsqlLiteralEngine(t)
	node, evaluation, issuer, usage := sepgsqlLiteralBinding(t, e)
	e.Mu.Lock()
	value, err := node.Eval(evaluation)
	e.Mu.Unlock()
	if err != nil || value.IsNull() || value.Type() != usage.Type {
		t.Fatal("real canonical constructor rejected", value, err)
	}
	e.Mu.Lock()
	codec := core.StatisticsSnapshotCodec(context.Background(), e, evaluation.Catalog)
	encoded, err := codec.Encode(usage, value)
	if err == nil {
		var decoded sqltype.Value
		decoded, err = codec.Decode(usage, encoded)
		if err == nil {
			var again []byte
			again, err = codec.Encode(usage, decoded)
			if err == nil && !bytes.Equal(encoded, again) {
				t.Error("canonical literal changed on real decode")
			}
		}
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("actual private canonical codec", err)
	}

	missing := *evaluation
	missing.ControlLiteralAuthority = exec.ControlLiteralAuthority{}
	if _, err = node.Eval(&missing); err == nil {
		t.Fatal("missing origin gained hostless active authority")
	}
	foreignEngine := sepgsqlLiteralEngine(t)
	_, _, foreignIssuer, _ := sepgsqlLiteralBinding(t, foreignEngine)
	foreign := *evaluation
	foreign.ControlLiteralAuthority = foreignIssuer.ControlLiteralAuthority()
	if _, err = node.Eval(&foreign); err == nil {
		t.Fatal("foreign codec origin reused same numeric routine identity")
	}

	// A retained origin is not authority for a fresh captured catalog root.
	enginetest.Run(t, e, "CREATE TABLE sepgsql_literal_new_root(id integer)")
	freshNode, fresh, _, _ := sepgsqlLiteralBinding(t, e)
	fresh.ControlLiteralAuthority = issuer.ControlLiteralAuthority()
	if _, err = freshNode.Eval(fresh); err == nil {
		t.Fatal("old codec origin rehabilitated against a fresh root")
	}

	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
	enginetest.RoleError(t, e, op, "SELECT hstore('k','v')", "42501", "SELinux: security policy violation", "")
	e.Mu.Lock()
	ordinary, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	borrowed := *fresh
	borrowed.Catalog = ordinary.Catalog
	borrowed.RoutineAuthority = &ordinary
	borrowed.ControlLiteralAuthority = fresh.ControlLiteralAuthority
	if _, err = freshNode.Eval(&borrowed); err == nil {
		t.Fatal("ordinary simulated client borrowed genuine private codec authority")
	}
	// An ordinary SQL bootstrap ROLE is not the private codec origin, even when
	// its real physical simulated subject is R and the selected C routine is real.
	forged := *fresh
	forged.Catalog = ordinary.Catalog
	forged.RoutineAuthority = &ordinary
	forged.ControlLiteralAuthority = exec.NewControlLiteralAuthority(ordinary.View)
	if _, err = freshNode.Eval(&forged); err == nil {
		t.Fatal("ordinary SQL authority minted a private literal grant")
	}
}
