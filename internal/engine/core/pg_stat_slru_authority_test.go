package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func TestPGStatSLRURoutineAuthority(t *testing.T) {
	e, other := engine.New(), engine.New()
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 2306}
	view := e.CatalogRead()
	p, ok := view.Procedure(address)
	q, exists := other.CatalogRead().Procedure(address)
	if !ok || !exists || p == q || p.Builtin == nil || p.Builtin != q.Builtin || p.Builtin != routine.BuiltinRoutine(2306) {
		t.Fatal("engine-owned descriptors and canonical immutable declaration", p, q)
	}
	if p.Builtin.Identity != (catalog.RoutineIdentity{ID: 2306, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_slru"}) || p.RoutineKind != catalog.FunctionRoutine || p.Result != 2249 || p.Strict || p.Volatility != expr.Stable || p.Builtin.Parallel != 'r' || !p.ReturnsSet() || p.RowEstimate() != 100 || p.CostEstimate() != 1 || p.Builtin.Defaults != 0 || p.Builtin.Variadic != 0 || p.Builtin.Providers != 0 {
		t.Fatal("released noncallable SLRU declaration", p, p.Builtin)
	}
	if p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: "pg_stat_get_slru"}) || p.ACL.Explicit || len(p.ACL.Entries) != 0 || p.Owner != e.Roles().LookupRef("postgres") || p.SQL != nil || p.SQLBody != nil || p.SQLDefaults != nil {
		t.Fatal("default PUBLIC authority and internal metadata", p)
	}
	outputs := p.OutputDeclaration()
	if outputs == nil || outputs != q.OutputDeclaration() || outputs.Len() != 9 || outputs.Inputs() != 0 {
		t.Fatal("owned immutable OUT declaration", outputs)
	}
	for i, name := range [...]string{"name", "blks_zeroed", "blks_hit", "blks_read", "blks_written", "blks_exists", "flushes", "truncates", "stats_reset"} {
		if outputs.Name(i) != name || outputs.Type(i) != ([...]ids.OID{25, 20, 20, 20, 20, 20, 20, 20, 1184})[i] {
			t.Fatal("released OUT field", i, outputs.Name(i), outputs.Type(i))
		}
	}
	if err := (stored.ProcedureProvider{}).Validate(view, p); err != nil {
		t.Fatal("catalog descriptor authority", err)
	}
	for _, provider := range []expr.RoutineProvider{functions.CatalogRoutineProvider, functions.OperatorRoutineProvider, functions.EmbeddedRoutineProvider, functions.IndexSupportRoutineProvider} {
		if _, err := routine.LookupBuiltinRoutineDeclaration(provider, p.Name(), nil); err == nil {
			t.Fatal("view backing acquired callable getter capability", provider)
		}
	}

	s := enginetest.StartSession(t, e, nil)
	family := e.Statistics.ReadSLRUs()
	for _, query := range []string{"SELECT pg_stat_get_slru()", "SELECT pg_catalog.pg_stat_get_slru()"} {
		for _, prepare := range []bool{false, true} {
			op := enginetest.TransactionOperation(t, s)
			ast := enginetest.Statement(t, query)
			var err error
			if prepare {
				_, err = e.Describe(op, ast)
			} else {
				result, failure := e.Execute(op, ast, 0, nil)
				err = failure
				if result.Rows != nil {
					result.Rows.Close()
					t.Fatal("unsupported getter published rows", query)
				}
			}
			op.Done()
			enginetest.Diagnostic(t, err, sqlstate.Error{Code: "0A000", Message: "functions are unsupported", Position: "8"})
			if e.Statistics.ReadSLRUs() != family {
				t.Fatal("unsupported getter changed provider/reset state", query)
			}
		}
	}

	activityOwnerRun(t, e, s, "CREATE ROLE slru_authority_reader", "SET stats_fetch_consistency=none")
	ast, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_slru")
	activityOwnerRun(t, e, s, "SET ROLE slru_authority_reader")
	result, err := enginetest.PreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal("default PUBLIC backing authority", err)
	}
	slruOwnerRows(t, result, family[0].ResetAt, [7]int64{})
	activityOwnerRun(t, e, s, "RESET ROLE", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_slru() FROM PUBLIC", "SET ROLE slru_authority_reader")
	for _, prepare := range []bool{false, true} {
		if prepare {
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
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_slru"})
	}
	activityOwnerRun(t, e, s, "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_slru() TO PUBLIC", "SET ROLE slru_authority_reader")
	result, err = enginetest.PreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal("live GRANT did not restore held view execution", err)
	}
	slruOwnerRows(t, result, family[0].ResetAt, [7]int64{})
	if e.Statistics.ReadSLRUs() != family {
		t.Fatal("ACL admission changed provider/reset state")
	}
}
