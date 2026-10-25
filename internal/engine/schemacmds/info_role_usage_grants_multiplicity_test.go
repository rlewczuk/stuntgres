package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
)

func TestInfoRoleUsageGrantsMultiplicity(t *testing.T) {
	f := newRoleUsageGrantsFixture(t)
	domain := *f.udt
	entry := defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)
	domain.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry, entry}}
	f.state.SetDescriptor(domain.Address(), &domain)
	want := roleUsageGrantsWant("default_owner", "default_reader", "USAGE", "NO")
	f.check(t, f.state, f.f.reader, want, want)
	// Even owner authority cannot create a new ACL origin from explicit emptiness.
	domain.ACL = authorization.ACLValue{Explicit: true}
	f.state.SetDescriptor(domain.Address(), &domain)
	f.check(t, f.state, f.f.owner)
	f.check(t, f.state, f.f.e.Roles().LookupRef("postgres"))
}
