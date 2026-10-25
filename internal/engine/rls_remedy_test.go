package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"testing"
)

// Persistent replacement reconstructs genuine release type/collation/callable
// declarations, but must not substitute user providers or altered lifetimes.
func TestRLSPolicyTemporaryBuiltinDependencies(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "CREATE TABLE runtime_helper(id INT)")
	sessionSQL(t, session, "CREATE TEMP TABLE runtime_policy(id INT)")
	sessionSQL(t, session, `CREATE POLICY p ON runtime_policy USING(id>0 AND random()<1 AND 'a' COLLATE "C"='a' AND EXISTS(SELECT 1 FROM runtime_helper WHERE runtime_helper.id=runtime_policy.id))`)
	current := e.CatalogRead()
	matches := current.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: session.TemporaryNamespace, Name: "runtime_policy"})
	if len(matches) != 1 {
		t.Fatal("missing temporary policy owner", matches)
	}
	owner := current.MustRelation(matches[0])
	next := catalog.CloneState(current.State())
	temporary, err := control.TemporarySet(current, next)
	if err != nil {
		t.Fatal(err)
	}
	var procedure, typ, collation ids.ObjectAddress
	for _, dependency := range stored.PolicyOf(owner.Security.Policies[0].Using).Presentation().Dependencies() {
		address := dependency.Ref.Address
		address.SubID = 0
		switch provider := current.Get(address).(type) {
		case *catalog.Procedure:
			if provider.Builtin == nil {
				continue
			}
			copy := *provider
			next.SetDescriptor(address, &copy)
			procedure = address
		case *catalog.Type:
			copy := *provider
			next.SetDescriptor(address, &copy)
			typ = address
		case *catalog.Collation:
			copy := *provider
			next.SetDescriptor(address, &copy)
			collation = address
		}
	}
	if procedure == (ids.ObjectAddress{}) || typ == (ids.ObjectAddress{}) || collation == (ids.ObjectAddress{}) {
		t.Fatal("fixture lacks actual builtin type/collation/callable manifest", typ, collation, procedure)
	}
	// Reconstruct the imported builtin generation too. The existing TEMP
	// survivor phase must restore the exact live codec before compatibility.
	imported := *next.MustType(typ)
	imported.SQLType = New().CatalogRead().MustType(typ).SQLType
	next.SetDescriptor(typ, &imported)
	if !control.RetainTemporaryBuiltinTypes(current, next, temporary) {
		t.Fatal("retained policy codec generation was not restored")
	}
	if err := control.CheckTemporaryDependencies(current, next, temporary); err != nil {
		t.Fatal("genuine reconstructed release declaration rejected", err)
	}
	helper := roleTableRef(t, e, "runtime_helper")
	for _, kind := range []string{"procedure", "type", "codec", "collation", "user_source"} {
		t.Run(kind, func(t *testing.T) {
			bad := catalog.CloneState(next)
			switch kind {
			case "procedure":
				copy := *bad.Get(procedure).(*catalog.Procedure)
				copy.Strict = !copy.Strict
				bad.SetDescriptor(procedure, &copy)
			case "type":
				copy := *bad.MustType(typ)
				copy.Array++
				bad.SetDescriptor(typ, &copy)
			case "codec":
				copy := *bad.MustType(typ)
				copy.SQLType = New().CatalogRead().MustType(typ).SQLType
				bad.SetDescriptor(typ, &copy)
			case "collation":
				copy := *bad.Get(collation).(*catalog.Collation)
				copy.Semantics++
				bad.SetDescriptor(collation, &copy)
			case "user_source":
				copy := *bad.MustRelation(helper.Address)
				bad.SetDescriptor(helper.Address, &copy)
			}
			if err := control.CheckTemporaryDependencies(current, bad, temporary); err == nil {
				t.Fatal("replacement accepted incompatible provider", kind)
			}
		})
	}
}

// Capturing a mutation root fixes snapshot membership, not a predicate read.
// A real INSERT SELECT still records the actual source's ordinary SSI read.
func TestRLSPolicySnapshotReadFootprint(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	for _, sql := range []string{"CREATE TABLE snapshot_target(id INT)", "CREATE TABLE snapshot_source(id INT)", "BEGIN ISOLATION LEVEL SERIALIZABLE", "INSERT INTO snapshot_target VALUES(1)"} {
		sessionSQL(t, session, sql)
	}
	defer sessionSQL(t, session, "ROLLBACK")
	target := roleTableRef(t, e, "snapshot_target")
	source := roleTableRef(t, e, "snapshot_source")
	e.Mu.Lock()
	node := e.Visibility.Serializable.Nodes[session.Transactions.Ref]
	valid := node != nil && node.Captured && node.DidWrite && len(node.Reads) == 0
	e.Mu.Unlock()
	if !valid {
		t.Fatal("target-only snapshot capture registered a read or omitted write")
	}
	sessionSQL(t, session, "INSERT INTO snapshot_target SELECT id FROM snapshot_source")
	e.Mu.Lock()
	node = e.Visibility.Serializable.Nodes[session.Transactions.Ref]
	_, readSource := node.Reads[source.Address]
	_, readTarget := node.Reads[target.Address]
	count := len(node.Reads)
	e.Mu.Unlock()
	if !readSource || readTarget || count != 1 {
		t.Fatal("snapshot union replaced actual source read footprint", readSource, readTarget, count)
	}
}
