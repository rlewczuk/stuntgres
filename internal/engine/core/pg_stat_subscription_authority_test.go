package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPGStatSubscriptionRoutineAuthority(t *testing.T) {
	e, other := engine.New(), engine.New()
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 6118}
	view := e.CatalogRead()
	p, ok := view.Procedure(address)
	q, exists := other.CatalogRead().Procedure(address)
	if !ok || !exists || p == q || p.Builtin == nil || p.Builtin != q.Builtin || p.Builtin != routine.BuiltinRoutine(6118) {
		t.Fatal("engine-owned descriptors and canonical immutable declaration", p, q)
	}
	if p.Builtin.Identity != (catalog.RoutineIdentity{ID: 6118, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_subscription", Signature: "26"}) || p.Signature != "26" || p.RoutineKind != catalog.FunctionRoutine || p.Result != 2249 || p.Strict || p.Volatility != expr.Stable || p.Builtin.Parallel != 'r' || !p.ReturnsSet() || p.RowEstimate() != 10 || p.CostEstimate() != 1 || p.Builtin.Defaults != 0 || p.Builtin.Variadic != 0 || p.Builtin.Providers != 0 {
		t.Fatal("released noncallable subscription declaration", p, p.Builtin)
	}
	if p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: "pg_stat_get_subscription"}) || p.ACL.Explicit || len(p.ACL.Entries) != 0 || p.Owner != e.Roles().LookupRef("postgres") || p.SQL != nil || p.SQLBody != nil || p.SQLDefaults != nil || p.InputNames != nil {
		t.Fatal("default PUBLIC authority and internal metadata", p)
	}
	outputs := p.OutputDeclaration()
	if outputs == nil || outputs != q.OutputDeclaration() || outputs.Len() != 10 || outputs.Inputs() != 1 || outputs.Name(0) != "subid" {
		t.Fatal("owned immutable IN/OUT declaration", outputs)
	}
	for i, name := range [...]string{"subid", "relid", "pid", "leader_pid", "received_lsn", "last_msg_send_time", "last_msg_receipt_time", "latest_end_lsn", "latest_end_time", "worker_type"} {
		if outputs.Name(i+1) != name || outputs.Type(i) != ([...]ids.OID{26, 26, 23, 23, 3220, 1184, 1184, 3220, 1184, 25})[i] {
			t.Fatal("released OUT field", i, outputs.Name(i+1), outputs.Type(i))
		}
	}
	if err := (stored.ProcedureProvider{}).Validate(view, p); err != nil {
		t.Fatal("catalog descriptor authority", err)
	}
	for _, provider := range []expr.RoutineProvider{functions.CatalogRoutineProvider, functions.OperatorRoutineProvider, functions.EmbeddedRoutineProvider, functions.IndexSupportRoutineProvider} {
		if _, err := routine.LookupBuiltinRoutineDeclaration(provider, p.Name(), []sqltype.ID{sqltype.Oid}); err == nil {
			t.Fatal("view backing acquired callable getter capability", provider)
		}
	}

	s := enginetest.StartSession(t, e, nil)
	key := resetStatsCreate(t, e, s, "status_authority")
	before := e.Statistics.Read(key)
	statsFunctionExec(t, s, "CREATE ROLE status_authority_reader", "SET stats_fetch_consistency=snapshot")
	ast, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_subscription")
	statsFunctionExec(t, s, "SET ROLE status_authority_reader")
	reader := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
	if !reader.IsZero() {
		t.Fatal("Describe initialized cumulative Reader")
	}
	result, err := enginetest.PreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal("default PUBLIC backing authority", err)
	}
	subscriptionStatusOwned(t, result, "status_authority")
	if !reader.IsZero() || e.Statistics.Read(key) != before {
		t.Fatal("admitted inactive status acquired cumulative/provider authority")
	}
	statsFunctionExec(t, s, "RESET ROLE", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) FROM PUBLIC", "SET ROLE status_authority_reader")
	for _, prepared := range []bool{false, true} {
		if prepared {
			result, err = enginetest.PreparedExecute(e, s, ast, &description)
		} else {
			op := enginetest.TransactionOperation(t, s)
			result, err = e.Execute(op, ast, 0, nil)
			op.Done()
		}
		if result.Rows != nil {
			result.Rows.Close()
			t.Fatal("denied backing authority published rows")
		}
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_subscription"})
		if !reader.IsZero() || e.Statistics.Read(key) != before {
			t.Fatal("denied inactive status changed Reader/provider ownership")
		}
	}
	emptyAST, emptyDescription := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_subscription WHERE FALSE")
	statsFunctionExec(t, s, "SELECT * FROM pg_stat_subscription WHERE FALSE", "SELECT * FROM pg_stat_subscription WHERE NULL::boolean")
	empty, err := enginetest.PreparedExecute(e, s, emptyAST, &emptyDescription)
	if err != nil {
		t.Fatal("undemanded getter required EXECUTE", err)
	}
	subscriptionStatusOwned(t, empty, "")
	if !reader.IsZero() || e.Statistics.Read(key) != before {
		t.Fatal("elided getter changed Reader/provider ownership")
	}
	statsFunctionExec(t, s, "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) TO PUBLIC", "SET ROLE status_authority_reader")
	result, err = enginetest.PreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	subscriptionStatusOwned(t, result, "status_authority")
	if !reader.IsZero() || e.Statistics.Read(key) != before {
		t.Fatal("regrant changed cumulative/provider ownership")
	}
}
