package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
)

func testWALRoutineMetadata(t *testing.T) {
	e, other := engine.New(), engine.New()
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1136}
	view := e.CatalogRead()
	p, ok := view.Procedure(address)
	q, exists := other.CatalogRead().Procedure(address)
	if !ok || !exists || p == q || p.Builtin == nil || p.Builtin != q.Builtin || p.Builtin != routine.BuiltinRoutine(1136) {
		t.Fatal("engine-owned descriptors and canonical immutable declaration", p, q)
	}
	if p.Builtin.Identity != (catalog.RoutineIdentity{ID: 1136, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_wal"}) || p.RoutineKind != catalog.FunctionRoutine || p.Result != 2249 || p.Strict || p.Volatility != expr.Stable || p.Builtin.Parallel != 'r' || p.ReturnsSet() || p.RowEstimate() != 0 || p.CostEstimate() != 1 || p.Builtin.Defaults != 0 || p.Builtin.Variadic != 0 || p.Builtin.Providers != 0 {
		t.Fatal("released noncallable WAL declaration", p, p.Builtin)
	}
	if p.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: "pg_stat_get_wal"}) || p.ACL.Explicit || len(p.ACL.Entries) != 0 || p.Owner != e.Roles().LookupRef("postgres") || p.SQL != nil || p.SQLBody != nil || p.SQLDefaults != nil {
		t.Fatal("default PUBLIC authority and internal metadata", p)
	}
	outputs := p.OutputDeclaration()
	if outputs == nil || outputs != q.OutputDeclaration() || outputs.Len() != 5 || outputs.Inputs() != 0 {
		t.Fatal("owned immutable OUT declaration", outputs)
	}
	for i, name := range [...]string{"wal_records", "wal_fpi", "wal_bytes", "wal_buffers_full", "stats_reset"} {
		if outputs.Name(i) != name || outputs.Type(i) != ([...]ids.OID{20, 20, 1700, 20, 1184})[i] {
			t.Fatal("released OUT field", i, outputs.Name(i), outputs.Type(i))
		}
	}
	if err := (stored.ProcedureProvider{}).Validate(view, p); err != nil {
		t.Fatal("catalog descriptor authority", err)
	}
	for _, provider := range []expr.RoutineProvider{functions.CatalogRoutineProvider, functions.OperatorRoutineProvider, functions.EmbeddedRoutineProvider, functions.IndexSupportRoutineProvider} {
		if _, err := routine.LookupBuiltinRoutineDeclaration(provider, p.Name(), nil); err == nil {
			t.Fatal("view backing acquired public getter capability", provider)
		}
	}
}
