package extension_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func refintCatalog(t *testing.T) (catalog.Read, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION refint")
	read := e.CatalogRead()
	ext := read.MustExtension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "refint"}))
	if len(ext.Members) != 2 || !ext.Installation.Valid() || len(ext.Installation.Members) != 2 || ext.Members[0] == ext.Members[1] {
		t.Fatal("invalid installed inventory", ext)
	}
	return read, ext
}

func TestRefintProviderIdentity(t *testing.T) {
	read, ext := refintCatalog(t)
	if err := extension.StockRefint.ValidateExtension(read, ext); err != nil {
		t.Fatal(err)
	}
	for slot, name := range []string{"check_primary_key", "check_foreign_key"} {
		p := read.MustProcedure(ext.Installation.Members[slot])
		if p.Name() != name || p.Signature != "" || p.Result != 2279 || p.Strict || p.Volatility != expr.Volatile || p.CostEstimate() != 1 || p.SQL != nil || p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'u'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/refint", HasBinary: true}) {
			t.Fatal("stock declaration differs", slot, p)
		}
		if symbol, err := extension.ProcedureSymbol(read, p); err != nil || symbol != name {
			t.Fatal("immutable symbol", symbol, err)
		}
		moved := *p
		moved.ObjectName = "renamed_" + name
		read.State().UnbindName(catalog.DescriptorNameKey(p))
		read.State().SetDescriptor(p.Address(), &moved)
		read.State().BindName(catalog.DescriptorNameKey(&moved), p.Address())
		if symbol, err := extension.ProcedureSymbol(read, &moved); err != nil || symbol != name {
			t.Fatal("rename lost declaration authority", symbol, err)
		}
		if _, err := extension.ProcedureSymbol(read, p); err == nil {
			t.Fatal("stale declaration accepted")
		}
	}
}

func TestRefintTriggerCapability(t *testing.T) {
	for _, kind := range []string{"genuine", "missing-origin", "wrong-source", "wrong-result", "wrong-signature", "strict", "wrong-slot", "copied-address"} {
		t.Run(kind, func(t *testing.T) {
			read, ext := refintCatalog(t)
			original := read.MustProcedure(ext.Members[0])
			p := *original
			switch kind {
			case "genuine":
				if !catalog.SupportsDMLTrigger(original) || !catalog.NativeDMLTrigger(original) || extension.StockRefint.ValidateProcedure(read, original) != nil {
					t.Fatal("genuine provider rejected")
				}
				return
			case "missing-origin":
				p.Origin = nil
			case "wrong-source":
				p.Display.Source = "check_foreign_key"
			case "wrong-result":
				p.Result = 25
			case "wrong-signature":
				p.Signature = "23"
			case "strict":
				p.Strict = true
			case "wrong-slot":
				p.Origin = read.MustProcedure(ext.Members[1]).Origin
			case "copied-address":
				p.ObjectAddress.ObjectID += 100
			}
			read.State().SetDescriptor(p.Address(), &p)
			if err := extension.StockRefint.ValidateProcedure(read, &p); err == nil {
				t.Fatal("forged native trigger authority accepted", kind)
			}
			if _, err := extension.ProcedureSymbol(read, &p); err == nil {
				t.Fatal("forged scalar symbol accepted", kind)
			}
		})
	}
}
