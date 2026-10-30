package authorization

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestRoleAttributesCyclicAdminTraversal(t *testing.T) {
	d := &Domain{}
	root := Seed(d)
	bootstrap := d.Principal(10)
	upstream := RoleDescriptor{
		Ref: d.Principal(100), Name: "upstream", Login: true, Superuser: true,
		CreateDB: true, CreateRole: true, Replication: true, BypassRLS: true,
		ConnectionLimit: 3,
	}
	member := RoleDescriptor{Ref: d.Principal(101), Name: "member", Inherit: true, ConnectionLimit: -1}
	middle := RoleDescriptor{Ref: d.Principal(102), Name: "middle", Inherit: true, ConnectionLimit: -1}
	target := RoleDescriptor{Ref: d.Principal(103), Name: "target", Inherit: true, ConnectionLimit: -1}
	for _, role := range []RoleDescriptor{upstream, member, middle, target} {
		root.roles[role.Ref] = role
		root.names[role.Name] = role.Ref
	}
	intoMiddle := Membership{Role: middle.Ref, Member: member.Ref, Grantor: bootstrap}
	intoMember := Membership{Role: member.Ref, Member: middle.Ref, Grantor: bootstrap}
	root.edges = append(root.edges,
		Membership{Role: upstream.Ref, Member: member.Ref, Grantor: bootstrap, Inherit: true, Set: true},
		intoMiddle,
		Membership{Role: target.Ref, Member: middle.Ref, Grantor: bootstrap, Admin: true},
		intoMember,
	)
	read := root.Read(bootstrap)
	hasIntoMiddle, hasIntoMember := false, false
	for edge := range read.Edges() {
		hasIntoMiddle = hasIntoMiddle || edge == intoMiddle
		hasIntoMember = hasIntoMember || edge == intoMember
	}
	if !hasIntoMiddle || !hasIntoMember {
		t.Fatal("fixture lacks the original disabled-option membership cycle")
	}
	if got, err := read.Descriptor(member.Ref); err != nil || got != member {
		t.Fatal("attributes inherited through cyclic membership", got, err)
	}
	if ok, err := read.InheritsRole(member.Ref, upstream.Ref); err != nil || !ok {
		t.Fatal("independent inherit path", ok, err)
	}
	if ok, err := read.AdminOfRole(member.Ref, target.Ref); err != nil || !ok {
		t.Fatal("cyclic ADMIN traversal", ok, err)
	}
	if ok, err := read.AdminOfRole(member.Ref, member.Ref); err != nil || ok {
		t.Fatal("cyclic self ADMIN", ok, err)
	}
	if ok, err := read.AdminOfRole(member.Ref, upstream.Ref); err != nil || ok {
		t.Fatal("cyclic SET/INHERIT supplied ADMIN", ok, err)
	}
	missing := d.Principal(104)
	foreign := (&Domain{}).Principal(member.Ref.ID())
	for _, endpoints := range []struct {
		name          string
		actor, target PrincipalRef
	}{
		{"zero target", member.Ref, PrincipalRef{}},
		{"zero actor", PrincipalRef{}, target.Ref},
		{"own zero target", member.Ref, d.Principal(0)},
		{"missing target", member.Ref, missing},
		{"missing actor", missing, target.Ref},
		{"foreign target", member.Ref, foreign},
		{"foreign actor", foreign, target.Ref},
		{"foreign self", foreign, foreign},
		{"superuser foreign target", bootstrap, foreign},
	} {
		if _, err := read.AdminOfRole(endpoints.actor, endpoints.target); err == nil {
			t.Fatal("cyclic ADMIN accepted invalid endpoint", endpoints.name)
		}
	}
	if ok, err := read.AdminOfRole(target.Ref, upstream.Ref); err != nil || ok {
		t.Fatal("cycle traversal leaked authority", ok, err)
	}
	if ok, err := read.CanSetRole(member.Ref, middle.Ref); err != nil || ok {
		t.Fatal("ADMIN path supplied SET", ok, err)
	}

	originalEdges := read.Memberships()
	adminState := RoleState{Authenticated: bootstrap, SessionUser: bootstrap, CurrentUser: bootstrap}
	memberState := RoleState{Authenticated: member.Ref, SessionUser: member.Ref, CurrentUser: member.Ref}
	login := &parser.AlterRole{Name: parser.Identifier{Text: "member"}, Options: parser.RoleOptions{Present: parser.RoleLogin, Login: true}}
	actor, descriptor, options, err := ValidateRoleAttributes(read, adminState, login)
	if err != nil || actor.Ref != bootstrap || descriptor != member {
		t.Fatal("direct LOGIN validation", actor, descriptor, err)
	}
	wantLoginMember := member
	wantLoginMember.Login = true
	loginMember := ApplyRoleOptions(descriptor, options)
	if loginMember != wantLoginMember {
		t.Fatal("direct LOGIN changed unrelated attributes", loginMember)
	}
	loginRoot, notices, err := AttributeCandidate(context.Background(), read, adminState, actor, loginMember, nil, CreatorSelfGrant{}, nil, false, false)
	if err != nil || len(notices) != 0 {
		t.Fatal("direct LOGIN candidate", notices, err)
	}
	loginRead := loginRoot.Read(bootstrap)
	for _, denied := range []struct {
		statement parser.Statement
		want      sqlstate.Error
	}{
		{
			&parser.CreateRole{Name: parser.Identifier{Text: "inherited_creation"}},
			sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501",
				Message: "permission denied to create role",
				Detail:  "Only roles with the CREATEROLE attribute may create roles.",
			},
		},
		{
			&parser.AlterRole{Name: parser.Identifier{Text: "target"}, Options: parser.RoleOptions{Present: parser.RoleLogin}},
			sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501",
				Message: "permission denied to alter role",
				Detail:  "Only roles with the CREATEROLE attribute and the ADMIN option on role \"target\" may alter this role.",
			},
		},
	} {
		_, _, _, err := ValidateRoleAttributes(loginRead, memberState, denied.statement)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || *diagnostic != denied.want {
			t.Fatal("cyclic membership inherited special authority", diagnostic, err, "want", denied.want)
		}
	}
	direct := &parser.AlterRole{Name: parser.Identifier{Text: "member"}, Options: parser.RoleOptions{Present: parser.RoleCreateRole | parser.RoleInherit, CreateRole: true}}
	actor, descriptor, options, err = ValidateRoleAttributes(loginRead, adminState, direct)
	if err != nil || descriptor != wantLoginMember {
		t.Fatal("direct CREATEROLE/NOINHERIT validation", descriptor, err)
	}
	wantDirectMember := wantLoginMember
	wantDirectMember.CreateRole = true
	wantDirectMember.Inherit = false
	directMember := ApplyRoleOptions(descriptor, options)
	if directMember != wantDirectMember {
		t.Fatal("direct options changed unrelated attributes", directMember)
	}
	directRoot, notices, err := AttributeCandidate(context.Background(), loginRead, adminState, actor, directMember, nil, CreatorSelfGrant{}, nil, false, false)
	if err != nil || len(notices) != 0 {
		t.Fatal("direct CREATEROLE/NOINHERIT candidate", notices, err)
	}
	directRead := directRoot.Read(bootstrap)
	alterTarget := &parser.AlterRole{Name: parser.Identifier{Text: "target"}, Options: parser.RoleOptions{Present: parser.RoleLogin, Login: true}}
	actor, descriptor, options, err = ValidateRoleAttributes(directRead, memberState, alterTarget)
	if err != nil || actor != wantDirectMember || descriptor != target {
		t.Fatal("direct CREATEROLE plus cyclic ADMIN did not authorize ALTER", actor, descriptor, err)
	}
	wantTarget := target
	wantTarget.Login = true
	alteredTarget := ApplyRoleOptions(descriptor, options)
	if alteredTarget != wantTarget {
		t.Fatal("target LOGIN changed unrelated attributes", alteredTarget)
	}
	targetRoot, notices, err := AttributeCandidate(context.Background(), directRead, memberState, actor, alteredTarget, nil, CreatorSelfGrant{}, nil, false, false)
	if err != nil || len(notices) != 0 {
		t.Fatal("authorized target candidate", notices, err)
	}
	current := targetRoot.Read(bootstrap)
	if got, err := current.Descriptor(target.Ref); err != nil || got != wantTarget {
		t.Fatal("target LOGIN candidate not retained", got, err)
	}
	if got, err := current.Descriptor(member.Ref); err != nil || got != wantDirectMember {
		t.Fatal("member direct attributes not retained", got, err)
	}
	for _, retained := range []struct {
		name   string
		read   Read
		member RoleDescriptor
	}{
		{"original", read, member},
		{"LOGIN", loginRead, wantLoginMember},
		{"CREATEROLE", directRead, wantDirectMember},
	} {
		if got, err := retained.read.Descriptor(member.Ref); err != nil || got != retained.member {
			t.Fatal("later candidate mutated retained member", retained.name, got, err)
		}
		if got, err := retained.read.Descriptor(target.Ref); err != nil || got != target {
			t.Fatal("later candidate mutated retained target", retained.name, got, err)
		}
	}
	for _, snapshot := range []Read{read, loginRead, directRead, current} {
		if !reflect.DeepEqual(originalEdges, snapshot.Memberships()) {
			t.Fatal("direct INHERIT change rewrote original cyclic membership order or bits")
		}
	}
	if ok, err := current.AdminOfRole(target.Ref, upstream.Ref); err != nil || ok {
		t.Fatal("cycle traversal leaked authority after direct attribute changes", ok, err)
	}
}
func TestAuthorizationDescriptorCandidateImmutability(t *testing.T) {
	d := &Domain{}
	root := Seed(d)
	old := root.Read(d.Principal(10))
	before, _ := old.Lookup("postgres")
	renamed := before
	renamed.Name = "original_bootstrap"
	next, err := root.PutDescriptor(renamed)
	if err != nil {
		t.Fatal(err)
	}
	current := next.Read(d.Principal(10))
	got, ok := current.Lookup("original_bootstrap")
	if !ok || got != renamed {
		t.Fatal(got)
	}
	if _, ok := current.Lookup("postgres"); ok {
		t.Fatal("obsolete name")
	}
	if got, ok := old.Lookup("postgres"); !ok || got != before || !reflect.DeepEqual(old.Memberships(), current.Memberships()) {
		t.Fatal("prior root changed")
	}
	replacement := RoleDescriptor{Ref: d.Principal(1000), Name: "postgres", Login: true}
	next, err = next.PutDescriptor(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := next.Read(d.Principal(10)).Lookup("postgres"); got.Ref == before.Ref || got.Predefined() {
		t.Fatal("name adopts fixed identity")
	}
	if _, err := old.Descriptor((&Domain{}).Principal(10)); err == nil {
		t.Fatal("foreign fixed id adopted")
	}
	if _, err := old.RoleClosure(context.Background(), PrincipalRef{}, true); err == nil {
		t.Fatal("zero shortcut")
	}
}

func TestPGGroupMalformedRoot(t *testing.T) {
	d := &Domain{}
	foreignDomain := &Domain{}
	parent := d.Principal(20000)
	middle := d.Principal(20001)
	leaf := d.Principal(20002)
	empty := d.Principal(20003)
	max := d.Principal(uint64(^uint32(0)))
	highParent := d.Principal(1<<32 | 20000)
	highMember := d.Principal(1<<32 | 20002)
	foreign := foreignDomain.Principal(20002)
	foreignParent := foreignDomain.Principal(20000)
	stale := d.Principal(21000)
	root := Root{
		domain: d,
		roles: map[PrincipalRef]RoleDescriptor{
			parent: {
				Ref: parent, Name: "parent", Superuser: true, CreateRole: true,
				CreateDB: true, Replication: true, BypassRLS: true,
				HasValidUntil: true, ValidUntil: -1 << 63,
			},
			middle:     {Ref: middle, Name: "middle"},
			leaf:       {Ref: leaf, Name: "leaf", Login: true},
			empty:      {Ref: empty, Name: "empty"},
			max:        {Ref: max, Name: "max"},
			highParent: {Ref: highParent, Name: "high"},
			highMember: {Ref: highMember, Name: "high-member", Login: true},
			foreign:    {Ref: foreign, Name: "foreign"},
		},
		names: map[string]PrincipalRef{},
	}
	for ref, role := range root.roles {
		root.names[role.Name] = ref
	}
	root.edges = []Membership{
		{Role: parent, Member: middle, Grantor: parent},
		{Role: middle, Member: leaf, Grantor: parent},
		{Role: parent, Member: leaf, Grantor: parent},
		{Role: parent, Member: leaf, Grantor: middle, Admin: true, Inherit: true, Set: true},
		{Role: parent, Member: max, Grantor: parent},
		{Role: highParent, Member: middle, Grantor: parent},
		{Role: parent, Member: highMember, Grantor: parent},
		{Role: parent, Member: foreign, Grantor: parent},
		{Role: parent, Member: stale, Grantor: parent},
		{Role: foreignParent, Member: leaf, Grantor: parent},
	}
	read := root.Read(parent)
	retainedEdges := read.Memberships()
	retainedParent := root.roles[parent]
	retainedLeaf := root.roles[leaf]
	if read.ValidReference(foreign) || read.ValidReference(PrincipalRef{}) {
		t.Fatal("malformed provenance accepted")
	}
	if _, err := read.Descriptor(stale); err == nil {
		t.Fatal("stale endpoint adopted")
	}
	if _, ok := read.Find(foreign); !ok {
		t.Fatal("raw enumeration hardened unrelated semantics")
	}
	for _, endpoint := range []struct {
		name          string
		ref           PrincipalRef
		group, member bool
	}{
		{"parent", parent, true, true},
		{"middle", middle, true, true},
		{"LOGIN leaf", leaf, false, true},
		{"empty", empty, true, true},
		{"max uint32", max, true, true},
		{"high parent", highParent, false, false},
		{"high member", highMember, false, false},
		{"present foreign descriptor", foreign, false, false},
	} {
		descriptor := root.roles[endpoint.ref]
		if got := read.GroupRole(endpoint.ref, descriptor); got != endpoint.group {
			t.Fatal("production group eligibility", endpoint.name, got)
		}
		if got := read.GroupMember(endpoint.ref); got != endpoint.member {
			t.Fatal("production member eligibility", endpoint.name, got)
		}
	}
	if read.GroupMember(stale) {
		t.Fatal("production member predicate accepted absent descriptor")
	}
	if _, ok := read.Find(foreignParent); ok {
		t.Fatal("foreign-parent edge unexpectedly has an enumerated descriptor")
	}
	loginDescriptor := root.roles[parent]
	loginDescriptor.Login = true
	loginRoot, err := root.PutDescriptor(loginDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	loginRead := loginRoot.Read(parent)
	if loginRead.GroupRole(parent, loginRoot.roles[parent]) || !read.GroupRole(parent, root.roles[parent]) || !loginRead.GroupMember(parent) {
		t.Fatal("LOGIN change altered old group eligibility or member eligibility")
	}
	// Preserve the original dangling middle->leaf tuple while deleting only
	// its descriptor. The actual consumer predicate must reject that member.
	withoutLeaf := cloneRoles(&root)
	delete(withoutLeaf.roles, leaf)
	missing := withoutLeaf.Read(parent)
	dangling := false
	for edge := range missing.Edges() {
		dangling = dangling || edge.Role == middle && edge.Member == leaf
	}
	if !dangling || !missing.GroupRole(middle, withoutLeaf.roles[middle]) || missing.GroupMember(leaf) || !read.GroupMember(leaf) {
		t.Fatal("production predicates did not exclude the dangling member")
	}
	if descriptor, ok := read.Find(leaf); !ok || descriptor != retainedLeaf {
		t.Fatal("missing-leaf candidate mutated retained descriptor")
	}
	withoutParent := cloneRoles(&root)
	delete(withoutParent.roles, parent)
	parentMissing := withoutParent.Read(parent)
	for ref := range parentMissing.RoleEntries() {
		if ref == parent {
			t.Fatal("deleted parent still enumerated")
		}
	}
	if parentMissing.GroupMember(parent) || parentMissing.GroupRole(highParent, withoutParent.roles[highParent]) || !parentMissing.GroupRole(middle, withoutParent.roles[middle]) || !read.GroupRole(parent, root.roles[parent]) {
		t.Fatal("missing parent aliased a wide identity or changed retained eligibility")
	}
	if _, ok := parentMissing.Find(foreignParent); ok {
		t.Fatal("missing parent adopted foreign same-ID identity")
	}
	// Each malformed key has an actual stored descriptor, so neither raw
	// enumeration nor a missing-map shortcut can supply the exclusion result.
	scalarRoot := cloneRoles(&loginRoot)
	scalarCases := []struct {
		name          string
		ref           PrincipalRef
		role          RoleDescriptor
		group, member bool
	}{
		{"zero value", PrincipalRef{}, RoleDescriptor{Name: "zero-value", Superuser: true}, false, false},
		{"own zero", d.Principal(0), RoleDescriptor{Ref: d.Principal(0), Name: "own-zero", Superuser: true}, false, false},
		{"mismatched descriptor", d.Principal(20004), RoleDescriptor{Ref: d.Principal(20005), Name: "mismatched", Superuser: true}, false, false},
		{"stored foreign parent", foreignParent, RoleDescriptor{Ref: foreignParent, Name: "foreign-parent", Superuser: true}, false, false},
		{"LOGIN parent", parent, loginDescriptor, false, true},
	}
	for _, scalar := range scalarCases {
		scalarRoot.roles[scalar.ref] = scalar.role
		scalarRoot.names[scalar.role.Name] = scalar.ref
	}
	scalarRead := scalarRoot.Read(parent)
	raw := make(map[PrincipalRef]RoleDescriptor)
	for ref, descriptor := range scalarRead.RoleEntries() {
		raw[ref] = descriptor
	}
	if len(raw) != len(root.roles)+4 || scalarRead.RoleCount() != len(raw) {
		t.Fatal("raw enumeration silently filtered malformed scalar fixtures")
	}
	for _, scalar := range scalarCases {
		stored, present := raw[scalar.ref]
		if !present || stored != scalar.role {
			t.Fatal("raw enumeration changed stored descriptor", scalar.name, stored)
		}
		if found, ok := scalarRead.Find(scalar.ref); !ok || found != scalar.role {
			t.Fatal("raw lookup hardened malformed storage", scalar.name, found)
		}
		if found, ok := scalarRead.Lookup(scalar.role.Name); !ok || found != scalar.role {
			t.Fatal("raw name lookup hardened malformed storage", scalar.name, found)
		}
		if got := scalarRead.GroupRole(scalar.ref, stored); got != scalar.group {
			t.Fatal("production scalar group eligibility", scalar.name, got)
		}
		if got := scalarRead.GroupMember(scalar.ref); got != scalar.member {
			t.Fatal("production scalar member eligibility", scalar.name, got)
		}
		if scalar.ref != parent {
			if _, present := read.Find(scalar.ref); present {
				t.Fatal("malformed scalar candidate changed retained root", scalar.name)
			}
		}
	}
	if !read.GroupRole(parent, retainedParent) || !read.GroupMember(parent) || scalarRead.GroupRole(parent, raw[parent]) {
		t.Fatal("LOGIN candidate replaced retained eligibility")
	}
	if descriptor, ok := read.Find(parent); !ok || descriptor != retainedParent {
		t.Fatal("descriptor candidates mutated retained parent")
	}
	for _, snapshot := range []Read{read, loginRead, missing, parentMissing, scalarRead} {
		if !reflect.DeepEqual(snapshot.Memberships(), retainedEdges) {
			t.Fatal("descriptor candidates changed original membership order/options/provenance")
		}
	}
}

func TestOwnershipDeletionCandidateValidation(t *testing.T) {
	domain := &Domain{}
	root := Seed(domain)
	old := root.Read(domain.Principal(10))
	before := old.Memberships()
	removed, err := OwnedMembershipCandidate(context.Background(), old, map[PrincipalRef]bool{domain.Principal(10): true})
	if err != nil || root.ValidateDeletionCandidate(&removed) != nil || len(removed.edges) != 0 {
		t.Fatal("strict deletion rejected", err)
	}
	if err := root.ValidateDeletionCandidate(&root); err == nil {
		t.Fatal("unchanged candidate accepted")
	}
	for _, kind := range []string{"reordered", "flags", "descriptor", "name"} {
		next := root
		next.edges = append([]Membership(nil), root.edges...)
		switch kind {
		case "reordered":
			next.edges[0], next.edges[1] = next.edges[1], next.edges[0]
		case "flags":
			next.edges[0].Set = !next.edges[0].Set
		case "descriptor":
			var e error
			d := root.roles[domain.Principal(10)]
			d.Comment = "changed"
			next, e = root.PutDescriptor(d)
			if e != nil {
				t.Fatal(e)
			}
		case "name":
			var e error
			d := root.roles[domain.Principal(10)]
			d.Name = "renamed"
			next, e = root.PutDescriptor(d)
			if e != nil {
				t.Fatal(e)
			}
		}
		if err := root.ValidateDeletionCandidate(&next); err == nil {
			t.Fatal("invalid ownership payload accepted", kind)
		}
	}
	if !reflect.DeepEqual(before, old.Memberships()) {
		t.Fatal("candidate validation mutated predecessor")
	}
}

func TestAuthorizationMalformedEnumeration(t *testing.T) {
	domain, foreignDomain := &Domain{}, &Domain{}
	good := domain.Principal(100)
	foreign := foreignDomain.Principal(100)
	zero := PrincipalRef{}
	root := Root{domain: domain, roles: map[PrincipalRef]RoleDescriptor{good: {Ref: domain.Principal(101), Name: "mismatched"}, foreign: {Ref: foreign, Name: "foreign", Superuser: true}, zero: {Ref: zero, Name: "zero", Superuser: true}}, names: map[string]PrincipalRef{"mismatched": good, "foreign": foreign, "zero": zero}}
	read := root.Read(good)
	if read.RoleCount() != 3 {
		t.Fatal("enumeration hardens unrelated projections")
	}
	for _, ref := range []PrincipalRef{zero, foreign} {
		if _, err := read.Descriptor(ref); err == nil {
			t.Fatal("invalid principal shortcut", ref)
		}
		if _, err := read.CanSetRole(ref, ref); err == nil {
			t.Fatal("SET invalid self/super shortcut", ref)
		}
	}
	descriptor, ok := read.Lookup("mismatched")
	if !ok || descriptor.Ref == good {
		t.Fatal("raw name/descriptor mismatch lost")
	}
	count := 0
	for range read.RoleEntries() {
		count++
	}
	if count != 3 {
		t.Fatal("private corrupted rows silently dropped")
	}
}
