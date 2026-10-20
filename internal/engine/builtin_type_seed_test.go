package engine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestBuiltinConversionProcedures(t *testing.T) {
	owner := (&authorization.Domain{}).Principal(10)
	first := builtinConversionProcedures(owner)
	if len(first) != len(builtinConversionRoutines) {
		t.Fatalf("got %d conversion procedures, want %d", len(first), len(builtinConversionRoutines))
	}
	pgCatalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	for i, routine := range builtinConversionRoutines {
		d := &first[i]
		provider, _ := sqltype.RegisteredEncodingConversion(routine.name)
		if d.Address() != (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: routine.oid}) || d.Name() != routine.name {
			t.Fatalf("procedure %d = %v %q, want %d %q", i, d.Address(), d.Name(), routine.oid, routine.name)
		}
		if d.Owner != owner || d.ObjectNamespace != pgCatalog || d.ObjectRevision != 1 || d.RoutineKind != catalog.FunctionRoutine ||
			d.Result != 23 || d.Volatility != expr.Immutable || !d.Strict || d.Conversion != provider {
			t.Fatalf("procedure %q has unexpected metadata: %+v", routine.name, d)
		}
	}
	second := builtinConversionProcedures(owner)
	if &first[0] == &second[0] {
		t.Fatal("each call must return fresh descriptors")
	}
}

func TestBuiltinDomainSeeds(t *testing.T) {
	checks, err := builtinDomainCheckSeeds()
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id   ids.OID
		name string
	}{{13256, "yes_or_no_check"}, {13243, "cardinal_number_domain_check"}}
	if len(checks) != len(want) {
		t.Fatalf("got %d domain CHECK seeds, want %d", len(checks), len(want))
	}
	for i, w := range want {
		seed := checks[i]
		if seed.id != w.id || len(seed.constraints) != 1 || seed.constraints[0].Check == nil || seed.constraints[0].Check.Name.Text != w.name {
			t.Fatalf("seed %d = %+v, want type %d with CHECK %q", i, seed, w.id, w.name)
		}
	}
	defaults := builtinDomainDefaultSeeds()
	stamp := parser.CurrentDateTimeExpr{Kind: parser.CurrentTimestamp, HasPrecision: true, Precision: 2}
	if len(defaults) != 1 || defaults[0].id != 13254 || defaults[0].expression != stamp {
		t.Fatalf("domain default seeds = %+v, want time_stamp (13254) DEFAULT CURRENT_TIMESTAMP(2)", defaults)
	}
}
