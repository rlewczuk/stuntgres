package core_test

import (
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"testing"
)

func pgstatindexUnitInstalled(t *testing.T, e *core.Engine) *catalog.Extension {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pgstattuple")
	return e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pgstattuple"}))
}
func TestPgstatindexProvider(t *testing.T) {
	e := engine.New()
	ext := pgstatindexUnitInstalled(t, e)
	view := e.CatalogRead()
	control := extension.StockPgstattuple.Control()
	if control.Version != "1.5" || control.Trusted || !control.Superuser || !control.Relocatable || control.Comment == nil || *control.Comment != "show tuple-level statistics" || len(ext.Members) != 3 {
		t.Fatal(control, ext)
	}
	if err := extension.StockPgstattuple.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	names := []string{"version", "tree_level", "index_size", "root_block_no", "internal_pages", "leaf_pages", "empty_pages", "deleted_pages", "avg_leaf_density", "leaf_fragmentation"}
	types := []ids.OID{23, 23, 20, 20, 20, 20, 20, 20, 701, 701}
	for i, address := range ext.Members {
		p := view.MustProcedure(address)
		sig := []catalog.ProcedureSignature{"25", "2205", "2205"}[i]
		source := []string{"pgstatindex_v1_5", "pgstatindexbyid_v1_5", "pgstathashindex"}[i]
		name := "pgstatindex"
		wantNames, wantTypes := names, types
		if i == 2 {
			name = "pgstathashindex"
			wantNames = []string{"version", "bucket_pages", "overflow_pages", "bitmap_pages", "unused_pages", "live_items", "dead_items", "free_percent"}
			wantTypes = []ids.OID{23, 20, 20, 20, 20, 20, 20, 701}
		}
		if p.Name() != name || p.Signature != sig || p.Result != 2249 || p.Volatility != expr.Volatile || !p.Strict || p.Installed.ReturnsSet || p.Installed.Parallel != 's' || p.Display.Source != source || p.Display.Binary != "$libdir/pgstattuple" || !p.Display.HasBinary || p.Display.Language != catalog.CRoutineLanguage {
			t.Fatal(p)
		}
		out := p.OutputDeclaration()
		if out == nil || out.Inputs() != 1 || out.Len() != len(wantNames) || out.Name(0) != "relname" {
			t.Fatal(out)
		}
		for j, name := range wantNames {
			if out.Name(j+1) != name || out.Type(j) != wantTypes[j] {
				t.Fatal(j, out)
			}
		}
		scan := view.Owner().Principals.Principal(authorization.StatScanTablesRoleID)
		ownerGrant, scanGrant := false, false
		if !p.ACL.Explicit {
			t.Fatal(p.ACL)
		}
		for _, acl := range p.ACL.Entries {
			if acl.Grantee.Public && acl.Privileges&authorization.ExecutePrivilege != 0 {
				t.Fatal("PUBLIC executable", p.ACL)
			}
			if acl.Grantor == p.Owner && acl.Privileges&authorization.ExecutePrivilege != 0 {
				ownerGrant = ownerGrant || acl.Grantee.Role == p.Owner
				scanGrant = scanGrant || acl.Grantee.Role == scan
			}
		}
		if !ownerGrant || !scanGrant {
			t.Fatal("actual owner and scan grants", p.ACL)
		}
		ref := e.Catalog.Domain.Ref(view.Epoch(), address)
		if err := extension.ValidatePgstattupleStatsIdentity(view, p, ref); err != nil {
			t.Fatal(err)
		}
		stale := ref
		stale.Epoch++
		if err := extension.ValidatePgstattupleStatsIdentity(view, p, stale); err == nil {
			t.Fatal("stale identity accepted")
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Signature = "23" }, func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Volatility = expr.Stable }, func(p *catalog.Procedure) { p.Implementation = new(extension.PgstattupleProvider) }, func(p *catalog.Procedure) { p.Installed.Parallel = 'r' }, func(p *catalog.Procedure) { p.Installed.ReturnsSet = true }, func(p *catalog.Procedure) { p.Display.Source = "pgstatindex" }, func(p *catalog.Procedure) {
				origin := *p.Origin
				origin.Declaration = 1 - origin.Declaration
				p.Origin = &origin
			},
		} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			state.SetDescriptor(address, &bad)
			if err := extension.StockPgstattuple.ValidateProcedure(e.Catalog.Read(state), &bad); err == nil {
				t.Fatal("forged release accepted", bad)
			}
		}
	}
	// Default grants unrelated to PUBLIC survive the extension script.
	defaults := engine.New()
	op, done := enginetest.RoleOperation(t, defaults, "postgres")
	defer done()
	enginetest.RoleExecute(t, defaults, op, "CREATE ROLE stats_default")
	enginetest.RoleExecute(t, defaults, op, "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO stats_default")
	enginetest.RoleExecute(t, defaults, op, "CREATE EXTENSION pgstattuple")
	role, _ := defaults.Roles().Lookup("stats_default")
	for _, address := range defaults.CatalogRead().MustExtension(defaults.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pgstattuple"})).Members {
		p := defaults.CatalogRead().MustProcedure(address)
		found := false
		for _, a := range p.ACL.Entries {
			found = found || a.Grantee.Role == role.Ref && a.Privileges&authorization.ExecutePrivilege != 0
		}
		if !found {
			t.Fatal("lost creation default", p.ACL)
		}
	}
	// Early and late signature collisions roll back every staged member.
	for _, signature := range []string{"pgstatindex(text)", "pgstatindex(regclass)", "pgstathashindex(regclass)"} {
		t.Run(signature, func(t *testing.T) {
			x := engine.New()
			op, finish := enginetest.RoleOperation(t, x, "postgres")
			defer finish()
			enginetest.RoleExecute(t, x, op, "CREATE FUNCTION "+signature+" RETURNS integer LANGUAGE SQL AS 'SELECT 7'")
			before := x.CatalogRead().State()
			_, err := x.Execute(op, enginetest.Statement(t, "CREATE EXTENSION pgstattuple"), 0, nil)
			if err == nil || x.CatalogRead().State() != before {
				t.Fatal("collision published partial installation", err)
			}
		})
	}
	// Owner export/import carries true ACLs rather than reconstructing role names.
	records, err := extension.OwnerMetadata(view, e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	destination := catalog.CloneState(view.State())
	if _, err := extension.ImportOwners(context.Background(), e.Catalog.Read(destination), e.Roles(), []extension.OwnerRecord{heapACLRecord(t, records)}); err != nil {
		t.Fatal(err)
	}
	for _, address := range ext.Members {
		if !destination.MustProcedure(address).ACL.Explicit {
			t.Fatal("ACL absence changed")
		}
	}
}

func TestPgstathashindexProvider(t *testing.T) {
	e := engine.New()
	ext := pgstatindexUnitInstalled(t, e)
	view := e.CatalogRead()
	p := view.MustProcedure(ext.Members[2])
	ref := e.Catalog.Domain.Ref(view.Epoch(), p.Address())
	foreign := engine.New()
	refForeign := foreign.Catalog.Domain.Ref(view.Epoch(), p.Address())
	if err := extension.ValidatePgstattupleStatsIdentity(view, p, refForeign); err == nil {
		t.Fatal("foreign domain accepted")
	}
	if err := extension.ValidatePgstattupleStatsIdentity(view, p, ref); err != nil { t.Fatal(err) }
	for _, slot := range []int{0, 1, 3} {
		state := catalog.CloneState(view.State())
		bad, origin := *p, *p.Origin
		origin.Declaration = slot
		bad.Origin = &origin
		state.SetDescriptor(p.Address(), &bad)
		if err := extension.StockPgstattuple.ValidateProcedure(e.Catalog.Read(state), &bad); err == nil { t.Fatal("forged hash slot accepted", slot) }
	}
}
