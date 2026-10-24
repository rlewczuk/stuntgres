package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestInfoRoleUdtGrantsPopulation(t *testing.T) {
	f := newRoleUdtGrantsFixture(t)
	owner := roleUdtGrantsWant("default_owner", "default_owner", "TYPE USAGE", "YES")
	public := roleUdtGrantsWant("default_owner", "PUBLIC", "TYPE USAGE", "NO")
	// A represented catalog row has reciprocal relation authority despite its
	// lookup-only codec. Relation ACLs never replace this row's type ACL.
	view := f.f.e.CatalogRead()
	class := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	lookup := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(class.RowType)})
	if lookup.SQLType.IsComposite() {
		t.Fatal("expected a genuinely lookup-only catalog row codec")
	}
	lookup.ObjectNamespace, lookup.Owner = f.f.schema, f.f.owner
	lookup.ACL, lookup.ACLSyntheticDefault = authorization.ACLValue{}, false
	state := catalog.CloneState(f.state)
	state.SetDescriptor(lookup.Address(), &lookup)
	f.checkType(t, state, f.f.owner, &lookup, owner, public)
	lookup.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)}}
	lookup.ACLSyntheticDefault = true
	state.SetDescriptor(lookup.Address(), &lookup)
	f.checkType(t, state, f.f.owner, &lookup, owner, public)
	f.checkType(t, state, f.f.reader, &lookup)
	lookup.ACLSyntheticDefault = false
	state.SetDescriptor(lookup.Address(), &lookup)
	f.checkType(t, state, f.f.reader, &lookup, roleUdtGrantsWant("default_owner", "default_reader", "TYPE USAGE", "NO"))

	for _, id := range []sqltype.ID{f.udt.Array, sqltype.Int4} {
		state := catalog.CloneState(f.state)
		excluded := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
		excluded.ObjectNamespace, excluded.Owner = f.f.schema, f.f.owner
		excluded.ACL, excluded.ACLSyntheticDefault = lookup.ACL, false
		state.SetDescriptor(excluded.Address(), &excluded)
		f.checkType(t, state, f.f.reader, &excluded)
	}
	op, done := roleOperation(t, f.f.e, "postgres")
	defer done()
	domainUnitPublish(t, f.f.e, domainUnitStage(t, f.f.e, op, "CREATE DOMAIN public.ug_domain AS public.ug_unit"))
	domain := *domainUnitType(t, f.f.e.CatalogRead(), "ug_domain")
	domain.ObjectNamespace, domain.Owner = f.f.schema, f.f.owner
	domain.ACL, domain.ACLSyntheticDefault = lookup.ACL, false
	state = catalog.CloneState(f.state)
	state.SetDescriptor(domain.Address(), &domain)
	f.checkType(t, state, f.f.reader, &domain)

	// Ordinary relation SELECT/USAGE entries cannot synthesize type origins.
	relation := *view.MustRelation(f.udt.OwningRelation)
	relation.Security.ACL = lookup.ACL
	state = catalog.CloneState(f.state)
	state.SetDescriptor(relation.Address(), &relation)
	f.check(t, state, f.f.reader)
}

func TestInfoRoleUdtGrantsReciprocalAuthority(t *testing.T) {
	for _, fault := range []string{"missing_relation", "wrong_row", "no_relation"} {
		t.Run(fault, func(t *testing.T) {
			f := newRoleUdtGrantsFixture(t)
			broken := *f.udt
			switch fault {
			case "missing_relation":
				broken.OwningRelation.ObjectID = 999999
			case "wrong_row":
				relation := *f.f.e.CatalogRead().MustRelation(broken.OwningRelation)
				relation.RowType = sqltype.Int4
				f.state.SetDescriptor(relation.Address(), &relation)
			case "no_relation":
				broken.OwningRelation = ids.ObjectAddress{}
			}
			f.state.SetDescriptor(broken.Address(), &broken)
			produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoRoleUdtGrantsOID)
			if !ok {
				t.Fatal("missing producer")
			}
			rows, err := produce(f.input(t, f.state, f.f.owner))
			if rows != nil || err == nil {
				t.Fatal("broken reciprocal authority accepted", rows, err)
			}
			var sqlState interface{ SQLState() string }
			if errors.As(err, &sqlState) {
				t.Fatal("invariant acquired SQLSTATE", err)
			}
		})
	}
}
