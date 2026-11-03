package extension_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestPgstatginindexProvider(t *testing.T) {
	e := engine.New()
	ext := pgstatindexUnitInstalled(t, e)
	view := e.CatalogRead()
	var p *catalog.Procedure
	for _, address := range ext.Members {
		candidate := view.MustProcedure(address)
		if candidate.Name() == "pgstatginindex" {
			if p != nil {
				t.Fatal("more than one GIN overload")
			}
			p = candidate
		}
	}
	if p == nil || len(ext.Members) != 9 {
		t.Fatal("missing single GIN declaration", ext)
	}
	if p.Signature != "2205" || p.Result != 2249 || !p.Strict || p.Volatility != expr.Volatile || p.Installed.ReturnsSet || p.Installed.Parallel != 's' || p.Display.Source != "pgstatginindex_v1_5" || p.Display.Binary != "$libdir/pgstattuple" || !p.Display.HasBinary || p.Display.Language != catalog.CRoutineLanguage {
		t.Fatal("declaration", p)
	}
	out := p.OutputDeclaration()
	if out == nil || out.Inputs() != 1 || out.Len() != 3 || out.Name(0) != "relname" {
		t.Fatal("output declaration", out)
	}
	for i, want := range []struct {
		name string
		typ  ids.OID
	}{{"version", 23}, {"pending_pages", 23}, {"pending_tuples", 20}} {
		if out.Name(i+1) != want.name || out.Type(i) != want.typ {
			t.Fatal("output slot", i, out)
		}
	}
	ownerGrant, scanGrant := false, false
	scan := view.Owner().Principals.Principal(authorization.StatScanTablesRoleID)
	for _, acl := range p.ACL.Entries {
		if acl.Grantee.Public && acl.Privileges&authorization.ExecutePrivilege != 0 {
			t.Fatal("PUBLIC EXECUTE", p.ACL)
		}
		if acl.Grantor == p.Owner && acl.Privileges&authorization.ExecutePrivilege != 0 {
			ownerGrant = ownerGrant || acl.Grantee.Role == p.Owner
			scanGrant = scanGrant || acl.Grantee.Role == scan
		}
	}
	if !p.ACL.Explicit || !ownerGrant || !scanGrant {
		t.Fatal("owner/scan ACL", p.ACL)
	}
	initial := view.State().InitialPrivilege(p.Address())
	if initial == nil || initial.PrivType != 'e' || !reflect.DeepEqual(initial.ACL, p.ACL) {
		t.Fatal("missing genuine initial ACL", initial)
	}
	ref := e.Catalog.Domain.Ref(view.Epoch(), p.Address())
	if err := extension.ValidatePgstattupleIdentity(view, p, ref); err != nil {
		t.Fatal(err)
	}
	foreign := engine.New()
	if err := extension.ValidatePgstattupleIdentity(view, p, foreign.Catalog.Domain.Ref(view.Epoch(), p.Address())); err == nil {
		t.Fatal("foreign provider owner accepted")
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Implementation = new(extension.PgstattupleProvider) },
		func(p *catalog.Procedure) { p.Display.Source = "pgstathashindex" },
		func(p *catalog.Procedure) { p.Signature = "25" },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Result = 23 },
		func(p *catalog.Procedure) {
			p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{23, 20, 20}, "relname", "version", "pending_pages", "pending_tuples")
		},
		func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 2; p.Origin = &origin },
	} {
		bad := *p
		mutate(&bad)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(p.Address(), &bad)
		if err := extension.StockPgstattuple.ValidateProcedure(e.Catalog.Read(state), &bad); err == nil {
			t.Fatal("forged GIN declaration accepted", bad)
		}
	}
	x := engine.New()
	op, done := enginetest.RoleOperation(t, x, "postgres")
	defer done()
	enginetest.RoleExecute(t, x, op, "CREATE FUNCTION pgstatginindex(regclass) RETURNS integer LANGUAGE SQL AS 'SELECT 7'")
	before := x.CatalogRead().State()
	if _, err := x.Execute(op, enginetest.Statement(t, "CREATE EXTENSION pgstattuple"), 0, nil); err == nil || x.CatalogRead().State() != before {
		t.Fatal("late collision published partial installation", err)
	}
}
