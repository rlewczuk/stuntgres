package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func ownershipTablefuncIdentity(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "tablefunc")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	original := before.MustProcedure(ext.Installation.Members[0])
	recipe := original.Origin.Installation
	for _, q := range []string{`ALTER EXTENSION tablefunc DROP FUNCTION public.normal_rand(integer,double precision,double precision)`, `ALTER FUNCTION public.normal_rand(integer,double precision,double precision) RENAME TO eo_normal`, `CREATE EXTENSION "uuid-ossp"`, `ALTER EXTENSION "uuid-ossp" ADD FUNCTION public.eo_normal(integer,double precision,double precision)`, `DROP EXTENSION tablefunc`, `ALTER EXTENSION "uuid-ossp" DROP FUNCTION public.eo_normal(integer,double precision,double precision)`} {
		enginetest.RoleExecute(t, e, op, q)
		view := e.CatalogRead()
		p := view.MustProcedure(original.Address())
		if p.Origin.Installation != recipe || p.Implementation != original.Implementation || p.Installed != original.Installed || p.Owner != original.Owner {
			t.Fatal("SQL membership/identity changes rewrote normal provider declaration", q)
		}
		if symbol, err := extension.ProcedureSymbol(view, p); err != nil || symbol != "normal_rand" {
			t.Fatal("normal provider lost immutable symbol", q, symbol, err)
		}
		if err := extension.StockTablefunc.ValidateProcedure(view, p); err != nil {
			t.Fatal("genuine normal procedure rejected", q, err)
		}
	}
	view := e.CatalogRead()
	p := view.MustProcedure(original.Address())
	if view.Get(ext.Address()) != nil || catalog.ExtensionMembership(p) != (ids.ObjectAddress{}) || before.MustProcedure(original.Address()) != original || original.Extension != ext.Address() {
		t.Fatal("absent/detached membership mutated immutable predecessor")
	}
	candidate := catalog.CloneState(view.State())
	forged := *p
	recipeCopy := *recipe
	origin := *p.Origin
	origin.Installation = &recipeCopy
	forged.Origin = &origin
	candidate.SetDescriptor(p.Address(), &forged)
	if err := extension.StockTablefunc.ValidateProcedure(view.WithState(candidate), &forged); err == nil {
		t.Fatal("normal provider accepted unsealed copied recipe")
	}
}
