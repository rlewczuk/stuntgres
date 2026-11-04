package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/sqlstate"
)

func sepgsqlMethodFactoryEngine(t *testing.T, label string) *core.Engine {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION bloom")
	policy, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{
		ID: "native-method-factory", Version: "1", Contexts: []string{"A", "R", "T", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "U",
		DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_procedure * P\n",
		Rules: []sepgsql.Rule{
			{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: "R", Target: "P", Class: "db_database", Allow: []string{"access"}},
			{Source: "R", Target: "P", Class: "db_schema", Allow: []string{"search"}},
			{Source: "R", Target: "P", Class: "db_procedure", Allow: []string{"execute"}},
			{Source: "R", Target: "S", Class: "db_procedure", Allow: []string{"entrypoint"}, Deny: []string{"execute"}},
			{Source: "R", Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition", "transition"}},
		},
		TrustedTransitions: []sepgsql.TrustedTransition{{Source: "R", Procedure: "S", Result: "T"}, {Source: "R", Procedure: "U", Result: "T"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(policy, true)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION blhandler(internal) IS '"+label+"'")
	return e
}

func TestSepgsqlAccessMethodFactoryOwners(t *testing.T) {
	e := sepgsqlMethodFactoryEngine(t, "S")
	foreign := sepgsqlMethodFactoryEngine(t, "S")
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
	read := e.CatalogRead()
	method, ok := read.LookupAccessMethod("bloom")
	if !ok {
		t.Fatal("missing genuine Bloom method")
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Catalog: read, Host: core.HostOf(scope)}}
	implementation, err := exec.InitializeIndexMethod(&evaluation, method.Address().ObjectID)
	if err != nil || implementation != catalog.BloomMethod {
		t.Fatal("real static factory did not return Bloom authority", implementation, err)
	}
	if op.Session.Sepgsql.Current() != "R" {
		t.Fatal("factory return leaked trusted body")
	}
	missing := evaluation
	missing.Host = nil
	missing.RoutineInitialization = exec.RoutineInitialization{}
	if _, err := exec.InitializeIndexMethod(&missing, method.Address().ObjectID); err == nil {
		t.Fatal("active hostless callback gained method authority")
	}
	closed := evaluation
	closed.ClosedEvaluation = &exec.EvaluationCapture{}
	if _, err := exec.InitializeIndexMethod(&closed, method.Address().ObjectID); err == nil {
		t.Fatal("closed callback silently borrowed live owner")
	}
	other := evaluation
	other.Catalog = foreign.CatalogRead()
	other.RoutineInitialization = exec.RoutineInitialization{}
	otherMethod, _ := other.Catalog.LookupAccessMethod("bloom")
	if _, err := exec.InitializeIndexMethod(&other, otherMethod.Address().ObjectID); err == nil {
		t.Fatal("foreign catalog borrowed numeric method identity")
	}
	canceled := evaluation
	ctx, cancel := context.WithCancel(op.Context())
	cancel()
	canceled.Context = ctx
	if _, err := exec.InitializeIndexMethod(&canceled, method.Address().ObjectID); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled factory returned authority", err)
	}
	for _, change := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.AccessMethod = catalog.BtreeAccessMethod },
		func(p *catalog.Procedure) { p.Implementation = nil },
	} {
		state := catalog.CloneState(read.State())
		handler, _ := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: method.Handler})
		bad := *handler
		change(&bad)
		state.SetDescriptor(bad.Address(), &bad)
		forged := evaluation
		forged.Catalog = read.WithState(state)
		if _, err := exec.InitializeIndexMethod(&forged, method.Address().ObjectID); err == nil {
			t.Fatal("metadata marker forged handler generation")
		}
	}
	scope.Finish(nil)
	finished = true
	if _, err := exec.InitializeIndexMethod(&evaluation, method.Address().ObjectID); err == nil {
		t.Fatal("ended owner retained factory authority")
	}
}

func TestSepgsqlAccessMethodFactoryDenied(t *testing.T) {
	e := sepgsqlMethodFactoryEngine(t, "U")
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
	read := e.CatalogRead()
	method, _ := read.LookupAccessMethod("bloom")
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Catalog: read, Host: core.HostOf(scope)}}
	got, err := exec.InitializeIndexMethod(&evaluation, method.Address().ObjectID)
	enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "SELinux: security policy violation"})
	if got != 0 || op.Session.Sepgsql.Current() != "R" {
		t.Fatal("denied factory published authority or subject", got)
	}
}

func TestSepgsqlOriginalAccessMethodFactoryIdentity(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	for _, oid := range []ids.OID{3, 330, 331, 332, 333, 334, 335} {
		p, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid})
		if !ok || !catalog.OriginalNativeAccessMethodProcedure(p) || !routine.FmgrBuiltin(p) {
			t.Fatal("missing original fmgr handler", oid)
		}
		moved := *p
		moved.ObjectName = "renamed_handler"
		moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90001}
		moved.ObjectRevision++
		if !catalog.OriginalNativeAccessMethodProcedure(&moved) || !routine.FmgrBuiltin(&moved) {
			t.Fatal("mutable presentation revoked original intrinsic identity", oid)
		}
		moved.ObjectAddress.ObjectID = 90002
		if catalog.OriginalNativeAccessMethodProcedure(&moved) || routine.FmgrBuiltin(&moved) {
			t.Fatal("new alias borrowed original intrinsic identity", oid)
		}
	}
}

func TestSepgsqlAccessMethodPrimaryKeyActor(t *testing.T) {
	t.Run("authorized_primary_key_creation", func(t *testing.T) {
		e := sepgsqlMethodFactoryEngine(t, "S")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		auth, err := core.OperationAuthorization(op)
		if err != nil {
			t.Fatal(err)
		}
		method, ok := auth.Catalog.LookupAccessMethod("btree")
		if !ok {
			t.Fatal("missing actual primary-key access method")
		}
		handler, ok := auth.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: method.Handler})
		if !ok {
			t.Fatal("missing actual primary-key factory declaration")
		}
		if _, err := auth.Roles.Descriptor(handler.Owner); err != nil {
			t.Fatal("primary-key factory lost its genuine declaration owner", err)
		}
		if auth.State.CurrentUser != op.RoleState().CurrentUser {
			t.Fatal("primary-key creation lost its actual SQL actor")
		}
		enginetest.RoleExecute(t, e, op, "CREATE TABLE sepgsql_truncate_parent(id integer PRIMARY KEY)")
		read := e.CatalogRead()
		namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		parent, ok := read.Relation(read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "sepgsql_truncate_parent"}))
		if !ok || parent.Owner != auth.State.CurrentUser || len(parent.Constraints.Unique) != 1 || !parent.Constraints.Unique[0].Primary {
			t.Fatal("authorized primary-key setup did not publish its real parent and constraint")
		}
		backing, ok := read.Relation(parent.Constraints.Unique[0].Index)
		if !ok || backing.Owner != auth.State.CurrentUser || backing.SQLIndex == nil || backing.SQLIndex.Parent != parent.Address() || backing.SQLIndex.MethodIdentity() != method.Address().ObjectID {
			t.Fatal("primary-key setup did not publish its actual owned btree index")
		}
		if root := e.Storage.IndexRoots[backing.Address()]; root == nil || len(root.Entries) != 0 {
			t.Fatal("empty primary-key setup lost its genuine index root")
		}
		enginetest.RoleExecute(t, e, op, "INSERT INTO sepgsql_truncate_parent VALUES(7)")
		root := e.Storage.IndexRoots[backing.Address()]
		if root == nil || len(root.Entries) != 1 {
			t.Fatal("authorized primary-key insertion did not maintain the actual root")
		}
		_, err = e.Execute(op, enginetest.Statement(t, "INSERT INTO sepgsql_truncate_parent VALUES(7)"), 0, nil)
		enginetest.Diagnostic(t, err, sqlstate.Error{
			Code: "23505", Message: `duplicate key value violates unique constraint "sepgsql_truncate_parent_pkey"`,
			Detail: "Key (id)=(7) already exists.", Schema: "public", Table: "sepgsql_truncate_parent",
			Constraint: "sepgsql_truncate_parent_pkey",
		})
		if e.Storage.IndexRoots[backing.Address()] != root {
			t.Fatal("rejected duplicate replaced the primary-key root")
		}
	})

	for _, invalid := range []string{"zero", "foreign"} {
		t.Run(invalid+"_source_actor", func(t *testing.T) {
			e := sepgsqlMethodFactoryEngine(t, "S")
			actor := authorization.PrincipalRef{}
			if invalid == "foreign" {
				foreign := engine.New()
				actor = foreign.Roles().LookupRef("postgres")
			}
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			finished := false
			defer func() {
				if !finished {
					scope.Finish(nil)
				}
			}()
			if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
				t.Fatal(err)
			}
			read := e.CatalogRead()
			method, ok := read.LookupAccessMethod("btree")
			if !ok {
				t.Fatal("missing genuine primary-key factory")
			}
			before, roots := read.State(), len(e.Storage.IndexRoots)
			op.Session.Mu.Lock()
			original := op.Session.Authorization.State
			op.Session.Authorization.State.CurrentUser = actor
			op.Session.Mu.Unlock()
			defer func() {
				op.Session.Mu.Lock()
				op.Session.Authorization.State = original
				op.Session.Mu.Unlock()
			}()
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Catalog: read, Host: core.HostOf(scope)}}
			physical, err := exec.InitializeIndexMethod(&evaluation, method.Address().ObjectID)
			if err == nil || err.Error() != "stuntgres: foreign or zero principal" || physical != 0 {
				t.Fatal("invalid source actor gained actual factory authority", physical, err)
			}
			op.Session.Mu.Lock()
			op.Session.Authorization.State = original
			op.Session.Mu.Unlock()
			scope.Finish(err)
			finished = true
			op.Session.Mu.Lock()
			op.Session.Authorization.State.CurrentUser = actor
			op.Session.Mu.Unlock()
			_, err = e.Execute(op, enginetest.Statement(t, "CREATE TABLE sepgsql_rejected_primary(id integer PRIMARY KEY)"), 0, nil)
			if err == nil || err.Error() != "stuntgres: foreign or zero principal" {
				t.Fatal("invalid source actor reached primary-key creation", err)
			}
			if e.CatalogRead().State() != before || len(e.Storage.IndexRoots) != roots || op.Session.Sepgsql.Current() != "A" {
				t.Fatal("rejected actor published catalog/index state or entered a trusted body")
			}
		})
	}
}

func TestSepgsqlAccessMethodFactorySources(t *testing.T) {
	t.Run("exact_ownerless_source", func(t *testing.T) {
		e := sepgsqlMethodFactoryEngine(t, "S")
		foreign := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(nil)
		if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
			t.Fatal(err)
		}
		host := core.HostOf(scope)
		owner, err := host.NewRoutineInitialization()
		if err != nil {
			t.Fatal(err)
		}
		read := e.CatalogRead()
		method, ok := read.LookupAccessMethod("bloom")
		if !ok {
			t.Fatal("missing actual ownerless method")
		}
		handler, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: method.Handler})
		if !ok {
			t.Fatal("missing actual factory handler")
		}
		site := exec.DirectRoutineInitializationSite{
			Routine: handler, Reference: read.Domain().Ref(read.Epoch(), handler.Address()),
			Purpose: exec.DirectAccessMethod, Source: read.Domain().Ref(read.Epoch(), method.Address()),
		}
		before, roots := read.State(), len(e.Storage.IndexRoots)
		if err := host.InitializeDirectRoutine(owner, site); err != nil {
			t.Fatal("genuine ownerless pg_am source rejected", err)
		}
		if err := host.ValidateDirectRoutineInitialization(owner, site); err != nil {
			t.Fatal("genuine ownerless pg_am entry rejected", err)
		}
		otherMethod, _ := read.LookupAccessMethod("btree")
		otherHandler, _ := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: otherMethod.Handler})
		heap, _ := read.LookupAccessMethod("heap")
		heapHandler, _ := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: heap.Handler})
		for _, test := range []struct {
			name   string
			change func(*exec.DirectRoutineInitializationSite)
		}{
			{"missing", func(s *exec.DirectRoutineInitializationSite) {
				s.Source = ids.ObjectRef{}
				s.Occurrence, s.ExpressionSource.Frame = 1, 1
			}},
			{"foreign", func(s *exec.DirectRoutineInitializationSite) {
				s.Source = foreign.CatalogRead().Domain().Ref(read.Epoch(), method.Address())
			}},
			{"stale_epoch", func(s *exec.DirectRoutineInitializationSite) { s.Source.Epoch++ }},
			{"subobject", func(s *exec.DirectRoutineInitializationSite) { s.Source.Address.SubID = 1 }},
			{"wrong_class", func(s *exec.DirectRoutineInitializationSite) { s.Source = s.Reference }},
			{"wrong_method", func(s *exec.DirectRoutineInitializationSite) {
				s.Source = read.Domain().Ref(read.Epoch(), otherMethod.Address())
			}},
			{"wrong_handler", func(s *exec.DirectRoutineInitializationSite) {
				s.Routine = otherHandler
				s.Reference = read.Domain().Ref(read.Epoch(), otherHandler.Address())
			}},
			{"wrong_purpose", func(s *exec.DirectRoutineInitializationSite) { s.Purpose = exec.DirectOrdering }},
			{"table_method", func(s *exec.DirectRoutineInitializationSite) {
				s.Routine = heapHandler
				s.Reference = read.Domain().Ref(read.Epoch(), heapHandler.Address())
				s.Source = read.Domain().Ref(read.Epoch(), heap.Address())
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				bad := site
				test.change(&bad)
				if err := host.InitializeDirectRoutine(owner, bad); err == nil {
					t.Fatal("forged factory source acquired an initialization receipt")
				}
				if err := host.ValidateDirectRoutineInitialization(owner, bad); err == nil {
					t.Fatal("rejected factory source retained an entry receipt")
				}
			})
		}
		if e.CatalogRead().State() != before || len(e.Storage.IndexRoots) != roots || op.Session.Sepgsql.Current() != "R" {
			t.Fatal("source admission published state or entered a handler body")
		}
	})

	t.Run("retired_source_cannot_reuse_receipt", func(t *testing.T) {
		e := sepgsqlMethodFactoryEngine(t, "S")
		enginetest.Run(t, e, "CREATE ACCESS METHOD sepgsql_factory_alias TYPE INDEX HANDLER bthandler")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(nil)
		if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
			t.Fatal(err)
		}
		host := core.HostOf(scope)
		owner, err := host.NewRoutineInitialization()
		if err != nil {
			t.Fatal(err)
		}
		read := e.CatalogRead()
		method, ok := read.LookupAccessMethod("sepgsql_factory_alias")
		if !ok {
			t.Fatal("missing genuine SQL-created source")
		}
		handler, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: method.Handler})
		if !ok {
			t.Fatal("missing genuine SQL-created source handler")
		}
		site := exec.DirectRoutineInitializationSite{
			Routine: handler, Reference: read.Domain().Ref(read.Epoch(), handler.Address()),
			Purpose: exec.DirectAccessMethod, Source: read.Domain().Ref(read.Epoch(), method.Address()),
		}
		if err := host.InitializeDirectRoutine(owner, site); err != nil {
			t.Fatal(err)
		}
		enginetest.Run(t, e, "DROP ACCESS METHOD sepgsql_factory_alias")
		if err := host.ValidateDirectRoutineInitialization(owner, site); err == nil {
			t.Fatal("retired source retained factory entry authority")
		}
		if err := host.InitializeDirectRoutine(owner, site); err == nil {
			t.Fatal("retired source reused a completed factory receipt")
		}
	})
}
