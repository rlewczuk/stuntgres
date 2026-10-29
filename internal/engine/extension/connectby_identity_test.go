package extension_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// This independently declared release inventory deliberately does not consume a
// production connectby manifest.
var tablefuncSelectedSignatures = []string{"23,701,701", "25,25,25,25,23,25", "25,25,25,25,23", "25,25,25,25,25,23,25", "25,25,25,25,25,23"}

func connectbyCatalog(t *testing.T) (catalog.Read, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA installed")
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA moved")
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION tablefunc SCHEMA installed")
	read := e.CatalogRead()
	ext := read.MustExtension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tablefunc"}))
	if len(ext.Members) != 16 || !ext.Installation.Valid() || len(ext.Installation.Members) != 16 {
		t.Fatal("incomplete genuine tablefunc recipe", ext)
	}
	for slot, signature := range tablefuncSelectedSignatures {
		proc := read.MustProcedure(ext.Installation.Members[slot])
		name, source, result, volatility := "connectby", "connectby_text", ids.OID(2249), expr.Stable
		if slot == 0 {
			name, source, result, volatility = "normal_rand", "normal_rand", 701, expr.Volatile
		} else if slot >= 3 {
			source = "connectby_text_serial"
		}
		if proc.Name() != name || string(proc.Signature) != signature || proc.Result != result || proc.Volatility != volatility || !proc.Strict || proc.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'u', ReturnsSet: true, Rows: 1000}) || proc.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: source, Binary: "$libdir/tablefunc", HasBinary: true}) || !proc.Origin.Valid(proc.Address()) {
			t.Fatal("independent released declaration mismatch", slot, proc)
		}
	}
	return read, ext
}
func TestTablefuncConnectbySelectedIdentity(t *testing.T) {
	v, ext := connectbyCatalog(t)
	if err := extension.StockTablefunc.ValidateExtension(v, ext); err != nil {
		t.Fatal(err)
	}
	for slot, address := range ext.Members[:len(tablefuncSelectedSignatures)] {
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
		moved.ObjectNamespace = v.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "moved"})
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
			v, ext := connectbyCatalog(t)
			p := *v.MustProcedure(ext.Members[1])
			if err := extension.StockTablefunc.ValidateExtension(v, ext); err != nil {
				t.Fatal("invalid baseline", err)
			}
			switch kind {
			case "count", "reorder", "duplicate":
				recipe := *ext.Installation
				recipe.Members = append([]ids.ObjectAddress(nil), recipe.Members...)
				switch kind {
				case "count":
					recipe.Members = recipe.Members[:4]
				case "reorder":
					recipe.Members[1], recipe.Members[2] = recipe.Members[2], recipe.Members[1]
				case "duplicate":
					recipe.Members[2] = recipe.Members[1]
				}
				origin := *p.Origin
				origin.Installation = &recipe
				p.Origin = &origin
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
	v, ext := connectbyCatalog(t)
	roleRoot := authorization.Seed(v.Owner().Principals)
	roles := roleRoot.Read(ext.Owner)
	metadata, err := extension.OwnerMetadata(v, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
	if err != nil || !found || len(record.Members) != 16 {
		t.Fatal("genuine sixteen-declaration metadata", record, found, err)
	}
	// The appended crosstab inventory is independent of production manifests.
	wants := []struct{ kind, name, signature string }{
		{"procedure", "crosstab", "pg_catalog.text"},
		{"type", "tablefunc_crosstab_2", ""},
		{"type", "_tablefunc_crosstab_2", ""},
		{"type", "tablefunc_crosstab_3", ""},
		{"type", "_tablefunc_crosstab_3", ""},
		{"type", "tablefunc_crosstab_4", ""},
		{"type", "_tablefunc_crosstab_4", ""},
		{"procedure", "crosstab2", "pg_catalog.text"},
		{"procedure", "crosstab3", "pg_catalog.text"},
		{"procedure", "crosstab4", "pg_catalog.text"},
		{"procedure", "crosstab", "pg_catalog.text, integer"},
	}
	seen := make([]bool, 16)
	for _, member := range record.Members {
		if member.Declaration == nil || *member.Declaration < 0 || *member.Declaration >= len(seen) || seen[*member.Declaration] {
			t.Fatal("invalid immutable owner inventory", member)
		}
		slot := *member.Declaration
		seen[slot] = true
		if member.Version != "1.0" || member.Schema != "installed" || member.Extension != "tablefunc" || member.Owner != "postgres" {
			t.Fatal("released current owner identity changed", member)
		}
		if slot >= 5 {
			want := wants[slot-5]
			if member.Kind != want.kind || member.Name != want.name || member.Signature != want.signature {
				t.Fatal("appended crosstab owner identity changed", slot, member, want)
			}
		}
	}
	decodeImport := func(r extension.OwnerRecord) error {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		decoded, found, err := extension.DecodeOwners(extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data))
		if !found {
			t.Fatal("owner marker not recognized")
		}
		if err != nil {
			return err
		}
		candidate := catalog.CloneState(v.State())
		_, err = extension.ImportOwners(context.Background(), v.WithState(candidate), roles, []extension.OwnerRecord{decoded})
		return err
	}
	if err := decodeImport(record); err != nil {
		t.Fatal("composed sixteen-member owner record", err)
	}
	for _, kind := range []string{"count", "signature", "declaration", "version"} {
		bad := record
		bad.Members = append([]extension.MemberOwner{}, record.Members...)
		switch kind {
		case "count":
			bad.Members = bad.Members[:4]
		case "signature":
			bad.Members[1].Signature = "25"
		case "declaration":
			bad.Members[1].Declaration = bad.Members[0].Declaration
		case "version":
			bad.Version = "missing"
		}
		if err := decodeImport(bad); err == nil {
			t.Fatal("invalid owner metadata accepted", kind)
		}
	}
}
