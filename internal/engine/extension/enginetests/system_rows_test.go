package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSystemRowsProviderIdentity(t *testing.T) {
	create := func() (catalog.Read, *catalog.Procedure) {
		e := engine.New()
		enginetest.Run(t, e, "CREATE EXTENSION tsm_system_rows")
		enginetest.Run(t, e, "CREATE SCHEMA sample_moved")
		read := e.CatalogRead()
		installation := read.MustExtension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tsm_system_rows"}))
		if len(installation.Members) != 1 {
			t.Fatal("not the one-member released recipe", installation)
		}
		return read, read.MustProcedure(installation.Members[0])
	}
	read, procedure := create()
	if err := extension.SystemRowsCapability(read, procedure); err != nil {
		t.Fatal(err)
	}
	if name, err := extension.ProcedureInstallationName(read, procedure); err != nil || name != "system_rows" {
		t.Fatal(name, err)
	}
	for _, change := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) {
			origin := *p.Origin
			installation := *origin.Installation
			origin.Installation = &installation
			p.Origin = &origin
		},
		func(p *catalog.Procedure) { p.Result = 23 },
		func(p *catalog.Procedure) { p.Signature = "23" },
		func(p *catalog.Procedure) { p.Display.Source = "system_rows" },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Installed.Parallel = 's' },
	} {
		forged := *procedure
		change(&forged)
		read.State().SetDescriptor(procedure.Address(), &forged)
		if err := extension.SystemRowsCapability(read, &forged); err == nil {
			t.Fatal("forged sampling capability admitted", forged)
		}
		read.State().SetDescriptor(procedure.Address(), procedure)
	}
	foreign, _ := create()
	if err := extension.SystemRowsCapability(foreign, procedure); err == nil {
		t.Fatal("foreign catalog authority admitted")
	}
	moved := *procedure
	moved.ObjectName = "renamed_sample"
	moved.ObjectNamespace = read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "sample_moved"})
	read.State().UnbindName(catalog.DescriptorNameKey(procedure))
	read.State().SetDescriptor(procedure.Address(), &moved)
	read.State().BindName(catalog.DescriptorNameKey(&moved), moved.Address())
	if err := extension.SystemRowsCapability(read, &moved); err != nil {
		t.Fatal("genuine rename/relocation lost capability", err)
	}
	if err := extension.SystemRowsCapability(read, procedure); err == nil {
		t.Fatal("stale original descriptor admitted")
	}
}

// The native catalog's typed procedure seam reaches the actual SELECT binder;
// it does not pretend to implement arbitrary external internal/C ABI bodies.
func TestSystemRowsHandlerResultBinding(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_rows")
	enginetest.Run(t, e, "CREATE TABLE sr(id integer)")
	read := e.CatalogRead()
	installation := read.MustExtension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tsm_system_rows"}))
	procedure := read.MustProcedure(installation.Members[0])
	wrong := *procedure
	wrong.Result = 23
	read.State().SetDescriptor(procedure.Address(), &wrong)
	defer read.State().SetDescriptor(procedure.Address(), procedure)
	raw := enginetest.Statement(t, "SELECT * FROM sr TABLESAMPLE system_rows(1)").(*parser.Select)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	bind := func() error {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			return err
		}
		binder := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
		binder.Query.Auth = &auth
		_, err = binder.BindSelect(raw, false)
		return err
	}
	enginetest.Diagnostic(t, bind(), sqlstate.Error{Code: "42809", Message: "function system_rows must return type tsm_handler", Position: "30"})
	read.State().SetDescriptor(procedure.Address(), procedure)
	if err := bind(); err != nil {
		t.Fatal("restored genuine handler lost binding authority", err)
	}
}
