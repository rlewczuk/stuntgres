package control

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

type relationACLWriteProbe struct{ calls int }

func (p *relationACLWriteProbe) Write(data []byte) (int, error) { p.calls++; return len(data), nil }

func TestAccessRelationsACLPreflight(t *testing.T) {
	for _, column := range []bool{false, true} {
		source, _, server, _ := fdwUnitSource()
		integer, ok := source.CatalogView.LookupType(sqltype.Int4)
		if !ok {
			t.Fatal("missing fixture int4")
		}
		r := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 95010), ObjectNamespace: fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID), ObjectName: "acl_preflight", ObjectRevision: 1, Owner: source.Bootstrap, RelationKind: catalog.ForeignRelation, Columns: []catalog.Column{{Name: "a", Usage: integer.Usage(), Nullable: true, NullabilityKnown: true}}, Attributes: []int32{1}, Index: map[string]int{"a": 0}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Foreign: &catalog.ForeignTableDefinition{Server: server.Address()}}
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: new(authorization.Domain).Principal(10), Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
		if column {
			acl.Entries[0].Grantor = source.Bootstrap
			acl.Entries[0].Privileges = authorization.DeletePrivilege
			r.Security.ColumnACL = map[int32]authorization.ACLValue{1: acl}
		} else {
			r.Security.ACL = acl
		}
		source.State.SetDescriptor(r.Address(), r)
		source.State.BindName(catalog.DescriptorNameKey(r), r.Address())
		writer := new(relationACLWriteProbe)
		if err := fdwUnitDump(source, writer); err == nil || writer.calls != 0 {
			t.Fatal("invalid raw ACL was not preflighted", column, err, writer.calls)
		}
	}
}
