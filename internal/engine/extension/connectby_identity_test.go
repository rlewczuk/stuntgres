package extension_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// This independently declared release inventory deliberately does not consume a
// production connectby manifest; it can precede the implementation it constrains.
var tablefuncSelectedSignatures = []string{"23,701,701", "25,25,25,25,23,25", "25,25,25,25,23", "25,25,25,25,25,23,25", "25,25,25,25,25,23"}

func connectbyCatalog() (catalog.Read, *catalog.Extension) {
	principals := &authorization.Domain{}
	owner := &catalog.Owner{Domain: &ids.Domain{}, Principals: principals, Extensions: map[string]catalog.ExtensionImplementation{"tablefunc": extension.StockTablefunc}}
	state := catalog.NewState(1)
	put := func(d catalog.Descriptor) {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 9000}
	put(&catalog.Namespace{ObjectAddress: ns, ObjectName: "installed", ObjectRevision: 1})
	put(&catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 9001}, ObjectName: "moved", ObjectRevision: 1})
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 9002}, ObjectName: "tablefunc", ObjectRevision: 1, Version: "1.0", TargetNamespace: ns, Owner: principals.Principal(10)}
	for slot, sig := range tablefuncSelectedSignatures {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(9100 + slot)}
		name, source, result, volatility := "connectby", "connectby_text", ids.OID(2249), expr.Stable
		if slot == 0 {
			name = "normal_rand"
			source = name
			result = 701
			volatility = expr.Volatile
		}
		if slot >= 3 {
			source = "connectby_text_serial"
		}
		put(&catalog.Procedure{ObjectAddress: address, ObjectNamespace: ns, ObjectName: name, ObjectRevision: 1, Extension: ext.Address(), Signature: catalog.ProcedureSignature(sig), Result: result, RoutineKind: catalog.FunctionRoutine, Owner: ext.Owner, Volatility: volatility, Strict: true, Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u', ReturnsSet: true, Rows: 1000}, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: source}, Implementation: extension.StockTablefunc})
		ext.Members = append(ext.Members, address)
	}
	put(ext)
	return catalog.NewRead(owner, state), ext
}
func TestTablefuncConnectbySelectedIdentity(t *testing.T) {
	v, ext := connectbyCatalog()
	if err := extension.StockTablefunc.ValidateExtension(v, ext); err != nil {
		t.Fatal(err)
	}
	for slot, address := range ext.Members {
		p := v.MustProcedure(address)
		want := "connectby"
		if slot == 0 {
			want = "normal_rand"
		}
		if symbol, err := extension.ProcedureSymbol(v, p); err != nil || symbol != want {
			t.Fatal(slot, symbol, err)
		}
		if err := extension.StockTablefunc.ValidateProcedure(v, p); err != nil {
			t.Fatal(slot, err)
		}
		old := p
		moved := *p
		moved.ObjectName = "renamed"
		moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 9001}
		moved.Owner = v.Owner().Principals.Principal(99)
		v.State().UnbindName(catalog.DescriptorNameKey(old))
		v.State().SetDescriptor(address, &moved)
		v.State().BindName(catalog.DescriptorNameKey(&moved), address)
		if symbol, err := extension.ProcedureSymbol(v, &moved); err != nil || symbol != want {
			t.Fatal("slot identity lost on relabel", slot, symbol, err)
		}
		if _, err := extension.ProcedureSymbol(v, old); err == nil {
			t.Fatal("stale descriptor admitted", slot)
		}
		ref := ids.ObjectRef{Address: address, Epoch: v.Epoch()}
		// Constructing a numeric ref cannot manufacture its owner-domain token.
		if err := extension.ValidateTablefuncIdentity(v, &moved, ref); err == nil {
			t.Fatal("foreign reference admitted", slot)
		}
	}
}
func TestTablefuncConnectbyManifestForgery(t *testing.T) {
	for _, kind := range []string{"count", "reorder", "duplicate", "signature", "result", "strict", "volatility", "source", "shape", "version"} {
		t.Run(kind, func(t *testing.T) {
			v, ext := connectbyCatalog()
			p := *v.MustProcedure(ext.Members[1])
			if err := extension.StockTablefunc.ValidateExtension(v, ext); err != nil {
				t.Fatal("invalid baseline", err)
			}
			switch kind {
			case "count":
				ext.Members = ext.Members[:4]
			case "reorder":
				ext.Members[1], ext.Members[2] = ext.Members[2], ext.Members[1]
			case "duplicate":
				ext.Members[2] = ext.Members[1]
			case "signature":
				p.Signature = "25,25,25,25,23"
			case "result":
				p.Result = 25
			case "strict":
				p.Strict = false
			case "volatility":
				p.Volatility = expr.Volatile
			case "source":
				p.Display.Source = "normal_rand"
			case "shape":
				p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{25}, "invented")
			case "version":
				ext.Version = "9.9"
			}
			v.State().SetDescriptor(p.Address(), &p)
			if err := extension.StockTablefunc.ValidateExtension(v, ext); err == nil {
				t.Fatal("corrupt manifest accepted")
			}
		})
	}
}
func TestTablefuncConnectbyOwnerCodec(t *testing.T) {
	record := extension.OwnerRecord{Name: "tablefunc", Schema: "installed", Version: "1.0", Owner: "postgres"}
	for slot, sig := range tablefuncSelectedSignatures {
		record.Members = append(record.Members, extension.MemberOwner{Slot: slot, Schema: "moved", Kind: "procedure", Name: "renamed", Signature: sig, Owner: "recipient"})
	}
	decode := func(r extension.OwnerRecord) error {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		_, found, err := extension.DecodeOwners(extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data))
		if !found {
			t.Fatal("owner marker not recognized")
		}
		return err
	}
	if err := decode(record); err != nil {
		t.Fatal("selected five-member owner record", err)
	}
	for _, kind := range []string{"count", "signature", "slot", "version"} {
		bad := record
		bad.Members = append([]extension.MemberOwner{}, record.Members...)
		switch kind {
		case "count":
			bad.Members = bad.Members[:4]
		case "signature":
			bad.Members[1].Signature = "25"
		case "slot":
			bad.Members[1].Slot = 2
		case "version":
			bad.Version = "missing"
		}
		if err := decode(bad); err == nil {
			t.Fatal("invalid owner metadata accepted", kind)
		}
	}
}
