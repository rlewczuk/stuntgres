package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Called by the provider-controls proof: exercise real associated-name collision
// and the owner codec through package APIs, without issuing public SQL.
func earthOwnerCodecUnits(t *testing.T) {
	e, _, _ := cubeUnitInstalled(t)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	oid, err := e.Catalog.AllocateOIDLocked()
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	arrayOID, err := e.Catalog.AllocateOIDLocked()
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	revision, err := e.Catalog.RevisionLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	shadow, err := sqltype.Domain(sqltype.ID(oid), "_earth", "public._earth", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	shadowArray, err := sqltype.Array(sqltype.ID(arrayOID), "__earth", shadow)
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	owner := op.RoleState().CurrentUser
	for _, typ := range []*catalog.Type{{SQLType: shadow, Array: shadowArray.ID(), ObjectNamespace: namespace, ObjectRevision: revision, Owner: owner}, {SQLType: shadowArray, ObjectNamespace: namespace, ObjectRevision: revision, Owner: owner}} {
		if err := stage.Create(typ); err != nil {
			t.Fatal(err)
		}
	}
	extensionUnitPublish(t, e, op, stage)
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "earthdistance", ""))
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "earthdistance"}))
	domain := view.MustType(ext.Members[0])
	array := view.MustType(ext.Members[1])
	if array.Name() == "_earth" || ids.OID(domain.Array) != array.Address().ObjectID {
		t.Fatal("missing actual companion collision")
	}
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	metadata, err := extension.OwnerMetadata(view, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(metadata, "-- "))
	if err != nil || !found || len(record.Members) != extension.EarthMemberCount || record.Members[0].Name != "earth" || record.Members[1].Name != "earth[]" {
		t.Fatal("symbolic companion roles", record, err)
	}
	candidate := *view.State()
	if _, err := extension.ImportOwners(context.Background(), catalog.NewRead(view.Owner(), &candidate), roles, []extension.OwnerRecord{record}); err != nil {
		t.Fatal("actual companion ownership resolution", err)
	}
	encode := func(record extension.OwnerRecord) string {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		return extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data)
	}
	for _, mutate := range []func(*extension.OwnerRecord){
		func(r *extension.OwnerRecord) { r.Version = "1.1" },
		func(r *extension.OwnerRecord) { r.Members[1].Name = "_earth" },
		func(r *extension.OwnerRecord) { r.Members[1] = r.Members[0] },
		func(r *extension.OwnerRecord) { r.Members[1].Owner = "inconsistent" },
		func(r *extension.OwnerRecord) { r.Members[extension.EarthPointProcedureSlot].Kind = "operator" },
		func(r *extension.OwnerRecord) { r.Members[extension.EarthPointProcedureSlot].Signature = "earth,earth" },
		func(r *extension.OwnerRecord) { r.Members[extension.EarthPointOperatorSlot].Kind = "procedure" },
		func(r *extension.OwnerRecord) { r.Members[extension.EarthPointOperatorSlot].Signature = "cube,cube" },
		func(r *extension.OwnerRecord) {
			r.Members[extension.EarthPointOperatorSlot].Slot = extension.EarthPointProcedureSlot
		},
	} {
		bad := record
		bad.Members = append([]extension.MemberOwner(nil), record.Members...)
		mutate(&bad)
		if _, found, err := extension.DecodeOwners(encode(bad)); !found || err == nil {
			t.Fatal("malformed owner metadata admitted", bad)
		}
	}
	bad := record
	bad.Members = append([]extension.MemberOwner(nil), record.Members...)
	bad.Members[1].Owner = "inconsistent"
	if _, err := extension.ImportOwners(context.Background(), catalog.NewRead(view.Owner(), &candidate), roles, []extension.OwnerRecord{bad}); err == nil {
		t.Fatal("inconsistent owner records bypassed decoder")
	}
}
