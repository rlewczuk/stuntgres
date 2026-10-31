package tables_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestTimezoneSourceDescriptors(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		oid   ids.OID
		names []string
		types []sqltype.ID
	}{
		{12005, []string{"name", "abbrev", "utc_offset", "is_dst"}, []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Interval, sqltype.Bool}},
		{12004, []string{"abbrev", "utc_offset", "is_dst"}, []sqltype.ID{sqltype.Text, sqltype.Interval, sqltype.Bool}},
	} {
		d := e.Catalog.State.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: tc.oid})
		if !d.VirtualView || d.View != nil || d.RelationKind != catalog.CatalogRelation || d.Namespace().ObjectID != 11 || len(d.Columns) != len(tc.names) {
			t.Fatal(d)
		}
		for i, c := range d.Columns {
			owner, ok := e.CatalogRead().LookupType(tc.types[i])
			if !ok || c.Name != tc.names[i] || c.Usage.Type != owner || c.Usage.Typmod != -1 || !c.Nullable || !c.NullabilityKnown || c.OriginTable != uint32(tc.oid) || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) {
				t.Fatal(i, c)
			}
		}
		if err := (core.RelationProvider).Validate(e.CatalogRead(), d); err != nil {
			t.Fatal(err)
		}
		if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
			t.Fatal(d.Security.ACL)
		}
		invalid := *d
		invalid.RelationKind = catalog.TableRelation
		if err := (core.RelationProvider).Validate(e.CatalogRead(), &invalid); err == nil {
			t.Fatal("virtual table accepted")
		}
		invalid = *d
		invalid.Columns = append([]catalog.Column(nil), d.Columns...)
		foreign, ok := engine.New().CatalogRead().LookupType(tc.types[0])
		if !ok {
			t.Fatal("foreign type missing")
		}
		invalid.Columns[0].Usage.Type = foreign
		if err := (core.RelationProvider).Validate(e.CatalogRead(), &invalid); err == nil {
			t.Fatal("foreign owner type accepted")
		}
	}
}
