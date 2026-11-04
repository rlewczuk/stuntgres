package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestPGLSNDiffRoutineDeclaration(t *testing.T) {
	// REL_18_6 pg_proc.dat OID3165 and pg_proc.h defaults, not native output.
	d := BuiltinRoutine(3165)
	identity := catalog.RoutineIdentity{ID: 3165, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_wal_lsn_diff", Signature: "3220,3220"}
	if d == nil || d.Identity != identity || d.Kind != catalog.FunctionRoutine || d.Result != 1700 || !d.Strict || d.Volatility != expr.Immutable || d.Parallel != 's' || d.ReturnsSet || d.Variadic != 0 || d.Defaults != 0 || d.Providers != functions.CatalogRoutineProvider || d.Outputs != nil {
		t.Fatalf("released LSN difference declaration: %+v", d)
	}
	got, err := LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, "pg_wal_lsn_diff", []sqltype.ID{sqltype.PgLSN, sqltype.PgLSN})
	if err != nil || got != d {
		t.Fatal("executable declaration authority", got, err)
	}
	if _, err := LookupBuiltinRoutineDeclaration(functions.OperatorRoutineProvider, "pg_wal_lsn_diff", []sqltype.ID{sqltype.PgLSN, sqltype.PgLSN}); err == nil {
		t.Fatal("identity lookup invented an operator provider")
	}
	if _, err := LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, "pg_wal_lsn_diff", []sqltype.ID{sqltype.Text, sqltype.PgLSN}); err == nil {
		t.Fatal("wrong formal signature admitted")
	}
	seen := false
	for _, p := range BuiltinProcedures(authorization.PrincipalRef{}) {
		if p.Address().ObjectID != 3165 {
			continue
		}
		seen = true
		if p.Builtin != d || p.Signature != identity.Signature || p.Result != 1700 || p.Cost != 1 || p.Display.Language != catalog.InternalRoutineLanguage || p.Display.Source != "pg_wal_lsn_diff" || p.InputNames != nil || p.LiteralDefaults != nil || p.DeclarationBody != nil || p.SecurityDefiner || p.ACL.Explicit {
			t.Fatal("published LSN difference metadata", p)
		}
	}
	if !seen {
		t.Fatal("missing executable procedure descriptor")
	}
}
