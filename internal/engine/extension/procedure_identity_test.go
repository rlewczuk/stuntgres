package extension

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"testing"
)

// Fixtures use unpublished states, but must publish ordinary name bindings
// explicitly just as real catalog staging does. SetDescriptor only stores the
// descriptor and its special default indexes.
func identityPut(state *catalog.State, descriptor catalog.Descriptor) {
	if previous := state.Get(descriptor.Address()); previous != nil {
		state.UnbindName(catalog.DescriptorNameKey(previous))
	}
	state.SetDescriptor(descriptor.Address(), descriptor)
	state.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
}

func identityFixture(version string) (catalog.Read, *catalog.Extension) {
	principals := &authorization.Domain{}
	owner := &catalog.Owner{Domain: &ids.Domain{}, Principals: principals, Extensions: map[string]catalog.ExtensionImplementation{"fuzzystrmatch": StockFuzzystrmatch, "pgcrypto": StockPgcrypto}}
	state := catalog.NewState(1)
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 9000}
	identityPut(state, &catalog.Namespace{ObjectAddress: ns, ObjectName: "installed", ObjectRevision: 1})
	moved := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 9001}
	identityPut(state, &catalog.Namespace{ObjectAddress: moved, ObjectName: "moved", ObjectRevision: 1})
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 9002}, ObjectName: "fuzzystrmatch", ObjectRevision: 1, Version: version, TargetNamespace: ns, Owner: principals.Principal(10)}
	start := 0
	if version == "1.1" {
		start = 1
	}
	for i, m := range FuzzystrmatchMembers[start:] {
		a := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(9100 + i)}
		ext.Members = append(ext.Members, a)
		identityPut(state, &catalog.Procedure{ObjectAddress: a, ObjectNamespace: ns, ObjectName: m.Name, ObjectRevision: 1, Extension: ext.Address(), Signature: m.Signature, Result: m.Result, RoutineKind: catalog.FunctionRoutine, Volatility: expr.Immutable, Strict: true, Owner: ext.Owner, Implementation: StockFuzzystrmatch})
	}
	identityPut(state, ext)
	return catalog.NewRead(owner, state), ext
}

func TestInstalledProcedureRelabelIdentity(t *testing.T) {
	for _, version := range []string{"1.1", "1.2"} {
		v, ext := identityFixture(version)
		p := v.MustProcedure(ext.Members[0])
		want := p.Name()
		moved := *p
		moved.ObjectName = "renamed"
		moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 9001}
		moved.Owner = v.Owner().Principals.Principal(99)
		identityPut(v.State(), &moved)
		if symbol, err := ProcedureSymbol(v, &moved); err != nil || symbol != want {
			t.Fatal(version, symbol, err)
		}
		if err := StockFuzzystrmatch.ValidateExtension(v, ext); err != nil {
			t.Fatal(err)
		}
		if ext.TargetNamespace == moved.Namespace() {
			t.Fatal("member move changed installation namespace")
		}
		if _, err := ProcedureSymbol(v, p); err == nil {
			t.Fatal("stale descriptor accepted")
		}
	}
}

func TestInstalledProcedureForgery(t *testing.T) {
	for _, kind := range []string{"signature", "result", "kind", "owner", "duplicate", "reorder", "foreign", "payload", "missing_namespace", "missing_index", "extension_owner"} {
		t.Run(kind, func(t *testing.T) {
			v, ext := identityFixture("1.2")
			p := *v.MustProcedure(ext.Members[0])
			if _, err := ProcedureSymbol(v, v.MustProcedure(p.Address())); err != nil {
				t.Fatal("invalid forgery baseline", err)
			}
			switch kind {
			case "signature":
				p.Signature = "23"
			case "result":
				p.Result = 23
			case "kind":
				p.RoutineKind = catalog.AggregateRoutine
			case "owner":
				p.Owner = (&authorization.Domain{}).Principal(10)
			case "duplicate":
				ext.Members[1] = ext.Members[0]
			case "reorder":
				ext.Members[0], ext.Members[1] = ext.Members[1], ext.Members[0]
			case "foreign":
				p.Extension.ObjectID++
			case "payload":
				p.Implementation = StockPgcrypto
			case "missing_namespace":
				p.ObjectNamespace.ObjectID++
				p.ObjectNamespace.ObjectID++
			case "extension_owner":
				ext.Owner = (&authorization.Domain{}).Principal(10)
			}
			identityPut(v.State(), &p)
			if kind == "missing_index" {
				v.State().UnbindName(catalog.DescriptorNameKey(&p))
			}
			if _, err := ProcedureSymbol(v, &p); err == nil {
				t.Fatal("forgery accepted")
			}
		})
	}
}

func TestInstalledProcedureVersionTransition(t *testing.T) {
	v, ext := identityFixture("1.1")
	incumbent := v.MustProcedure(ext.Members[0])
	renamed := *incumbent
	renamed.ObjectName = "old_member_renamed"
	identityPut(v.State(), &renamed)
	m := FuzzystrmatchMembers[0]
	a := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 9999}
	identityPut(v.State(), &catalog.Procedure{ObjectAddress: a, ObjectNamespace: ext.TargetNamespace, ObjectName: m.Name, ObjectRevision: 1, Extension: ext.Address(), Signature: m.Signature, Result: m.Result, RoutineKind: catalog.FunctionRoutine, Volatility: expr.Immutable, Strict: true, Owner: ext.Owner, Implementation: StockFuzzystrmatch})
	ext.Members = append([]ids.ObjectAddress{a}, ext.Members...)
	ext.Version = "1.2"
	if symbol, err := ProcedureSymbol(v, &renamed); err != nil || symbol != "levenshtein" {
		t.Fatal(symbol, err)
	}
	if err := StockFuzzystrmatch.ValidateExtension(v, ext); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionOwnerSlotCodec(t *testing.T) {
	record := OwnerRecord{Name: "pgcrypto", Schema: "installed", Version: "1.4", Owner: "postgres"}
	for i, m := range functions.PgcryptoMembers {
		record.Members = append(record.Members, MemberOwner{Slot: i, Schema: "moved", Kind: "procedure", Name: "renamed_" + m.Name, Signature: m.Signature, Owner: "recipient"})
	}
	decode := func(r OwnerRecord) error {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		_, found, err := DecodeOwners(OwnersMarker + " " + base64.StdEncoding.EncodeToString(data))
		if !found {
			t.Fatal("marker not recognized")
		}
		return err
	}
	if err := decode(record); err != nil {
		t.Fatal(err)
	}
	record.Members[1].Slot = 0
	if err := decode(record); err == nil {
		t.Fatal("duplicate slot accepted")
	}
	record.Members[1].Slot = 1
	record.Members[0].Schema = ""
	if err := decode(record); err == nil {
		t.Fatal("missing schema accepted")
	}
	if _, found, err := DecodeOwners("stuntgres-extension-owners-v1 e30="); !found || err == nil {
		t.Fatal("obsolete name-only codec accepted")
	}
}

func TestInstalledProcedureRemapIdentity(t *testing.T) {
	v, ext := identityFixture("1.2")
	selected := v.MustProcedure(ext.Members[0])
	renamed := *selected
	renamed.ObjectName = "renamed"
	renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 9001}
	identityPut(v.State(), &renamed)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
	for a := range v.Descriptors() {
		next := a
		next.ObjectID += 10000
		mapping[a] = next
	}
	state := catalog.NewState(2)
	for a, d := range v.Descriptors() {
		if ns, ok := d.(*catalog.Namespace); ok {
			n := *ns
			n.ObjectAddress = mapping[a]
			identityPut(state, &n)
		} else {
			n := RemapDescriptor(d, mapping)
			identityPut(state, n)
		}
	}
	current := v.WithState(state)
	p := current.MustProcedure(mapping[selected.Address()])
	if symbol, err := ProcedureSymbol(current, p); err != nil || symbol != "daitch_mokotoff" {
		t.Fatal(symbol, err)
	}
	if _, err := ProcedureSymbol(current, &renamed); err == nil {
		t.Fatal("source address accepted after remap")
	}
	parent := current.MustExtension(mapping[ext.Address()])
	if parent.Members[0] != p.Address() || ext.Members[0] != selected.Address() {
		t.Fatal("slot order or source changed")
	}
}

func TestExtensionOwnerImportCurrentSlotIdentity(t *testing.T) {
	v, ext := identityFixture("1.2")
	root := authorization.Seed(v.Owner().Principals)
	roles := root.Read(ext.Owner)
	p := *v.MustProcedure(ext.Members[0])
	p.ObjectName = "renamed"
	p.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 9001}
	identityPut(v.State(), &p)
	metadata, err := OwnerMetadata(v, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := ImportOwners(context.Background(), catalog.NewRead(v.Owner(), catalog.CloneState(v.State())), roles, []OwnerRecord{record}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"name", "schema", "signature", "duplicate", "foreign_slot"} {
		t.Run(kind, func(t *testing.T) {
			bad := record
			bad.Members = append([]MemberOwner(nil), record.Members...)
			switch kind {
			case "name":
				bad.Members[0].Name = "old_name"
			case "schema":
				bad.Members[0].Schema = "installed"
			case "signature":
				bad.Members[0].Signature = "23"
			case "duplicate":
				bad.Members[1].Slot = 0
			case "foreign_slot":
				bad.Members[0].Slot = 99
			}
			if _, err := ImportOwners(context.Background(), catalog.NewRead(v.Owner(), catalog.CloneState(v.State())), roles, []OwnerRecord{bad}); err == nil {
				t.Fatal("corrupt current identity accepted")
			}
		})
	}
}

func TestDetachedUnaccentImmutableCapability(t *testing.T) {
	v, _ := identityFixture("1.2")
	for i, signature := range []catalog.ProcedureSignature{catalog.UnaccentTextSignature, catalog.UnaccentDictionarySignature, catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature} {
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(9800 + i)}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 9001}, ObjectName: "renamed", ObjectRevision: 1, Owner: v.Owner().Principals.Principal(10), Implementation: StockUnaccent, Signature: signature, RoutineKind: catalog.FunctionRoutine, Result: 25, Strict: true, Volatility: expr.Stable}
		want := "unaccent"
		if i >= 2 {
			p.Result, p.Strict, p.Volatility = 2281, false, expr.Volatile
			want = "unaccent_init"
			if i == 3 {
				want = "unaccent_lexize"
			}
		}
		identityPut(v.State(), p)
		if symbol, err := procedureSymbol(v, p, true); err != nil || symbol != want {
			t.Fatal(symbol, err)
		}
		if _, err := ProcedureSymbol(v, p); err == nil {
			t.Fatal("detached metadata gained installed execution identity")
		}
		p.Result++
		if _, err := procedureSymbol(v, p, true); err == nil {
			t.Fatal("forged detached shape accepted")
		}
		v.State().UnbindName(catalog.DescriptorNameKey(p))
		v.State().RemoveDescriptor(p.Address())
	}
}

func TestExtensionOwnerImportTransfer(t *testing.T) {
	v, ext := identityFixture("1.2")
	root := authorization.Seed(v.Owner().Principals)
	recipient := v.Owner().Principals.Principal(99)
	next, err := root.PutDescriptor(authorization.RoleDescriptor{Ref: recipient, Name: "recipient"})
	if err != nil {
		t.Fatal(err)
	}
	roles := next.Read(ext.Owner)
	metadata, err := OwnerMetadata(v, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	if err != nil {
		t.Fatal(err)
	}
	record.Members[0].Owner = "recipient"
	candidate := catalog.CloneState(v.State())
	if changed, err := ImportOwners(context.Background(), catalog.NewRead(v.Owner(), candidate), roles, []OwnerRecord{record}); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if candidate.MustProcedure(ext.Members[0]).Owner != recipient || v.MustProcedure(ext.Members[0]).Owner != ext.Owner {
		t.Fatal("owner transfer lost or mutated source")
	}
}
