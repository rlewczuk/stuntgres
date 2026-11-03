package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestBackendMemoryTemporaryBuiltinRetention(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	// SQL only constructs genuine live ownership/storage roots. Assertions below
	// concern private retention/import identity, not duplicated SQL outcomes.
	enginetest.SessionSQL(t, session, "CREATE TEMP TABLE memory_retention(saved pg_catalog.pg_backend_memory_contexts,saved_array pg_catalog.pg_backend_memory_contexts[])")
	enginetest.SessionSQL(t, session, "INSERT INTO memory_retention SELECT m,ARRAY[m] FROM pg_catalog.pg_backend_memory_contexts m WHERE name='TopMemoryContext'")
	current := e.CatalogRead()
	oldRow, oldArray := backendMemoryNominalPair(t, current)
	owners := current.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: session.TemporaryNamespace, Name: "memory_retention"})
	if len(owners) != 1 { t.Fatal("missing actual live TEMP owner", owners) }
	temporaryOwner := current.MustRelation(owners[0])
	if temporaryOwner.Columns[0].Usage.Type != oldRow || temporaryOwner.Columns[1].Usage.Type != oldArray { t.Fatal("fixture lacks genuine nominal roots") }
	fresh := engine.New()
	next := catalog.CloneState(fresh.CatalogRead().State())
	temporary, err := control.TemporarySet(current, next)
	if err != nil { t.Fatal(err) }
	if temporary[temporaryOwner.Address()] != temporaryOwner { t.Fatal("actual TEMP owner was not retained") }
	changed, retained := control.RetainTemporaryBuiltinTypes(current, next, temporary)
	if !changed || retained[12144] != oldRow || retained[12143] != oldArray { t.Fatal("retention lost the exact destination pair") }
	for i := range oldRow.CompositeLen() {
		typ := oldRow.CompositeMember(i).Usage.Type
		if retained[typ.ID()] != typ || next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}).SQLType != typ { t.Fatal("dynamic member closure not retained", i) }
		if typ.IsArray() && retained[typ.Element().ID()] != typ.Element() { t.Fatal("array element closure not retained", i) }
	}
	for id, typ := range retained {
		for _, peerID := range sqltype.BuiltinGenerationPeers(id) {
			peer := current.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(peerID)}).SQLType
			if peer.IsDomain() || peer.IsArray() && peer.Element().IsDomain() { continue }
			if retained[peerID] != peer { t.Fatal("static generation peer not retained", id, peerID) }
		}
		if current.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}).SQLType != typ { t.Fatal("retention recorded unproven source pointer", id) }
	}
	if again, evidence := control.RetainTemporaryBuiltinTypes(current, next, temporary); again || evidence[12144] != oldRow || evidence[12143] != oldArray { t.Fatal("identical candidate lost retention evidence", again) }
	beforeImport := catalog.CloneState(next)
	if _, err := typedef.ReownDomainTypes(next, nil, true, routine.RangeTypeRebinder(next, nil), retained); err != nil { t.Fatal(err) }
	if err := stored.ReownScalarTypes(next, nil, nil); err != nil { t.Fatal(err) }
	row, array := backendMemoryNominalPair(t, e.Catalog.Read(next))
	if row != oldRow || array != oldArray || array.Element() != oldRow { t.Fatal("import reminted live TEMP codecs") }
	for id, typ := range retained { if next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}).SQLType != typ { t.Fatal("import reminted retained generation", id) } }
	if old, companion := backendMemoryNominalPair(t, current); old != oldRow || companion != oldArray { t.Fatal("retention/import mutated current source") }
	for _, problem := range []string{"partial_pair", "missing_member", "changed_member", "mapped_row", "mapped_namespace"} {
		t.Run(problem, func(t *testing.T) {
			candidate := catalog.CloneState(beforeImport)
			evidence := make(map[sqltype.ID]*sqltype.Descriptor, len(retained))
			for id, typ := range retained { evidence[id] = typ }
			mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
			switch problem {
			case "partial_pair": delete(evidence, 12143)
			case "missing_member": delete(evidence, sqltype.Int4)
			case "changed_member":
				address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Text)}
				copy := *candidate.MustType(address)
				copy.SQLType = fresh.CatalogRead().MustType(address).SQLType
				candidate.SetDescriptor(address, &copy)
			case "mapped_row": mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12144}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 50000}
			case "mapped_namespace": mapping[ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}] = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 50000}
			}
			if _, err := typedef.ReownDomainTypes(candidate, mapping, true, routine.RangeTypeRebinder(candidate, mapping), evidence); err == nil { t.Fatal("invalid retained generation accepted", problem) }
			if e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12144}).SQLType != oldRow { t.Fatal("rejected candidate reached current catalog") }
		})
	}
}
