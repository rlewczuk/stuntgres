package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func fileFDWInstalled(t *testing.T) (*core.Engine, catalog.Read, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION file_fdw")
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "file_fdw"})
	ext, ok := view.Extension(address)
	if !ok || !ext.Installation.Valid() || len(ext.Members) != 3 { t.Fatal("missing genuine staged installation") }
	return e, view, ext
}

func TestFileFDWInstalledIdentity(t *testing.T) {
	installed, view, ext := fileFDWInstalled(t)
	for _, slot := range []int{0, 1} {
		if err := extension.StockFileFDW.ValidateProcedure(view, view.MustProcedure(ext.Members[slot])); err != nil { t.Fatal(err) }
	}
	if err := extension.StockFileFDW.ValidateForeign(view, view.MustForeign(ext.Members[2])); err != nil { t.Fatal(err) }
	for _, tc := range []struct { name string; change func(*catalog.Procedure) }{
		{"same name without provenance", func(p *catalog.Procedure) { p.Origin = nil; p.Implementation = nil }},
		{"copied capability", func(p *catalog.Procedure) { token := *p.Foreign; p.Foreign = &token }},
		{"different provider", func(p *catalog.Procedure) { p.Implementation = extension.StockInsertUsername }},
		{"released symbol", func(p *catalog.Procedure) { p.Display.Source = "file_fdw_handler" }},
		{"result", func(p *catalog.Procedure) { p.Result = 16 }},
		{"strict", func(p *catalog.Procedure) { p.Strict = false }},
		{"volatility", func(p *catalog.Procedure) { p.Volatility = expr.Immutable }},
		{"foreign owner domain", func(p *catalog.Procedure) { p.Owner = new(authorization.Domain).Principal(p.Owner.ID()) }},
		{"copied installation", func(p *catalog.Procedure) { origin := *p.Origin; copied := *origin.Installation; origin.Installation = &copied; p.Origin = &origin }},
		{"wrong declaration", func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 0; p.Origin = &origin }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(view.State())
			p := *view.MustProcedure(ext.Members[1])
			tc.change(&p)
			state.SetDescriptor(p.Address(), &p)
			if err := extension.StockFileFDW.ValidateProcedure(view.WithState(state), &p); err == nil { t.Fatal("forgery admitted") }
		})
	}
	for _, tc := range []struct { name string; change func(*catalog.ForeignObject) }{
		{"handler link", func(w *catalog.ForeignObject) { w.Handler = ext.Members[1] }},
		{"validator link", func(w *catalog.ForeignObject) { w.Validator = ext.Members[0] }},
		{"wrapper options", func(w *catalog.ForeignObject) { w.Options = []catalog.ForeignOption{{Name: "filename", Value: "/x"}} }},
		{"wrapper origin", func(w *catalog.ForeignObject) { origin := *w.Origin; origin.Declaration = 0; w.Origin = &origin }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(view.State())
			w := *view.MustForeign(ext.Members[2])
			tc.change(&w)
			state.SetDescriptor(w.Address(), &w)
			replaced := view.WithState(state)
			if err := extension.StockFileFDW.ValidateProcedure(replaced, replaced.MustProcedure(ext.Members[1])); err == nil { t.Fatal("one-sided wrapper graph admitted") }
		})
	}
	canonical := ext.Installation.Declarations[1].(*catalog.Procedure)
	for _, q := range []string{"CREATE ROLE private_file_owner", "CREATE SCHEMA private_file_schema", "ALTER FUNCTION file_fdw_validator(text[],oid) RENAME TO private_file_validator", "ALTER FUNCTION private_file_validator(text[],oid) SET SCHEMA private_file_schema", "ALTER FUNCTION private_file_schema.private_file_validator(text[],oid) OWNER TO private_file_owner", "ALTER FOREIGN DATA WRAPPER file_fdw RENAME TO private_file_wrapper"} { enginetest.Run(t, installed, q) }
	current := installed.CatalogRead()
	validator := current.MustProcedure(ext.Members[1])
	if validator.Origin.Installation != ext.Installation || validator.Origin.Declaration != 1 || canonical.Name() != "file_fdw_validator" || canonical.Namespace() != ext.TargetNamespace || canonical.Owner == validator.Owner || validator.Foreign != canonical.Foreign { t.Fatal("mutable labels rewrote canonical capability backing") }
	if err := extension.StockFileFDW.ValidateProcedure(current, validator); err != nil { t.Fatal(err) }
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for _, a := range append([]ids.ObjectAddress{ext.Address()}, ext.Installation.Members...) { moved := a; moved.ObjectID += 200000; mapping[a] = moved }
	next := catalog.CloneState(current.State())
	if err := control.RemapReplacementIdentities(current, next, nil, mapping, nil, nil, nil); err != nil { t.Fatal(err) }
	remapped := current.WithState(next)
	moved := remapped.MustProcedure(mapping[validator.Address()])
	wrapper := remapped.MustForeign(mapping[ext.Members[2]])
	if moved.Foreign != validator.Foreign || moved.Origin.Installation == validator.Origin.Installation || moved.Origin.Installation != wrapper.Origin.Installation || moved.Origin.Installation.Cohort != mapping[ext.Address()] || wrapper.Validator != moved.Address() { t.Fatal("replacement lost coherent recipe/token ownership") }
	if err := extension.StockFileFDW.ValidateProcedure(remapped, moved); err != nil { t.Fatal(err) }
	if validator.Origin.Installation.Declarations[1] != canonical || validator.Origin.Installation.Members[1] != validator.Address() { t.Fatal("replacement mutated original immutable recipe") }
	t.Run("replacement_principal_authority", func(t *testing.T) {
		target := engine.New()
		enginetest.Run(t, target, "CREATE ROLE private_file_owner")
		candidate := catalog.CloneState(current.State())
		if err := fdw.RebindReplacementProviders(target.Catalog.Read(candidate), target.Foreign, candidate); err == nil { t.Fatal("destination domain authenticated unmapped source principals") }
		sourceRoles, targetRoles := installed.Roles(), target.Roles()
		target.Mu.Lock()
		prepared, _, err := core.EngineRemapPreparedCatalog(target, current.WithState(candidate), sourceRoles, targetRoles, nil)
		target.Mu.Unlock()
		if err != nil { t.Fatal("source-owned pre-remap authentication", err) }
		destination := target.Catalog.Read(prepared)
		restored := destination.MustProcedure(validator.Address())
		if restored.Owner == validator.Owner || restored.Owner != targetRoles.LookupRef("private_file_owner") || installed.Roles().ValidatePrincipal(restored.Owner) == nil || targetRoles.ValidatePrincipal(restored.Owner) != nil { t.Fatal("replacement adopted the wrong owner domain") }
		if err := fdw.RebindReplacementProviders(destination, target.Foreign, prepared); err != nil { t.Fatal("destination-owned post-remap authentication", err) }
		if err := extension.StockFileFDW.ValidateProcedure(destination, restored); err != nil { t.Fatal(err) }
		if err := extension.StockFileFDW.ValidateForeign(destination, destination.MustForeign(ext.Members[2])); err != nil { t.Fatal(err) }
		if validator.Owner == restored.Owner || validator.Origin.Installation.Declarations[1] != canonical { t.Fatal("replacement mutated retained source authority") }
	})
	t.Run("constructor_created_identity", func(t *testing.T) {
		for _, mode := range []string{"unvalidated", "stock_validated"} {
			t.Run(mode, func(t *testing.T) {
				e := engine.New()
				wrapper, options := "private_empty_wrapper", ""
				if mode == "stock_validated" {
					enginetest.Run(t, e, "CREATE EXTENSION file_fdw")
					wrapper, options = "file_fdw", " OPTIONS(filename '/not-a-real-constructor-source')"
				} else {
					enginetest.Run(t, e, "CREATE FOREIGN DATA WRAPPER private_empty_wrapper")
				}
				enginetest.Run(t, e, "CREATE SERVER private_created_server FOREIGN DATA WRAPPER "+wrapper)
				created := enginetest.Run(t, e, "CREATE FOREIGN TABLE private_created(id integer) SERVER private_created_server"+options).Created
				read := e.CatalogRead()
				namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
				address := read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "private_created"})
				if created == (ids.ObjectRef{}) || created.Domain() != e.Catalog.Domain || created.Epoch != read.Epoch() || created.Address != address { t.Fatal("constructor lost its actual created reference", created, address) }
				if e.Storage.Relations[address] != nil { t.Fatal("created foreign relation acquired a heap") }
				if skipped := enginetest.Run(t, e, "CREATE FOREIGN TABLE IF NOT EXISTS private_created(id integer) SERVER private_created_server"+options); skipped.Created != (ids.ObjectRef{}) { t.Fatal("skipped creation fabricated a reference", skipped.Created) }
				failed, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "CREATE FOREIGN TABLE private_failed(id integer,id integer) SERVER private_created_server"+options), nil)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42701" || diagnostic.Message != `column "id" specified more than once` || failed.Created != (ids.ObjectRef{}) { t.Fatal("duplicate-column creation lost its diagnostic or returned a reference", failed.Created, err) }
				if e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "private_failed"}) != (ids.ObjectAddress{}) { t.Fatal("failed creation leaked metadata") }
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				canceled, err := enginetest.Execute(e, ctx, enginetest.Statement(t, "CREATE FOREIGN TABLE private_canceled(id integer) SERVER private_created_server"+options), nil)
				if !errors.Is(err, context.Canceled) || canceled.Created != (ids.ObjectRef{}) { t.Fatal("canceled creation returned a reference", canceled.Created, err) }
			})
		}
	})
}
