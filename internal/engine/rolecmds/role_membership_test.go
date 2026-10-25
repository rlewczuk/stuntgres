package rolecmds_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Fixtures use the real private command planner, with checked identities and
// explicit options; there is no second append-only membership publication path.
func grantMembership(t *testing.T, e *core.Engine, m authorization.Membership) error {
	t.Helper()
	r := e.Roles()
	names := make([]string, 3)
	for i, ref := range []authorization.PrincipalRef{m.Role, m.Member, m.Grantor} {
		d, err := r.Descriptor(ref)
		if err != nil {
			return err
		}
		names[i] = d.Name
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	spec := func(name string) parser.RoleSpec { return parser.RoleSpec{Name: name} }
	g := spec(names[2])
	stmt := &parser.RoleMembership{Targets: []parser.RoleSpec{spec(names[0])}, Members: []parser.RoleSpec{spec(names[1])}, Grantor: &g, Options: []parser.MembershipOption{{Name: "admin", Value: m.Admin}, {Name: "inherit", Value: m.Inherit}, {Name: "set", Value: m.Set}}}
	_, err := e.Execute(op, stmt, 0, nil)
	return err
}

func TestMembershipImmutableDefaultsAndAtomicity(t *testing.T) {
	e := engine.New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE ROLE r", "CREATE ROLE m", "CREATE ROLE x"} {
		roleExecute(t, e, op, q)
	}
	oldPublication := e.RoleStore.Published
	old := e.Roles()
	revision := oldPublication.Revision
	roleExecute(t, e, op, "GRANT r TO m")
	readPublication := e.RoleStore.Published
	read := e.Roles()
	edges := read.Memberships()
	if len(edges) != 4 || edges[3].Admin || !edges[3].Inherit || !edges[3].Set || readPublication.Revision != revision+1 || len(old.Memberships()) != 3 {
		t.Fatal(edges, readPublication.Revision, revision)
	}
	edges[3].Inherit = false
	if !read.Memberships()[3].Inherit {
		t.Fatal("borrowed edge mutation")
	}
	roleExecute(t, e, op, "GRANT r TO m WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
	roleExecute(t, e, op, "GRANT r TO m WITH INHERIT TRUE")
	nowPublication := e.RoleStore.Published
	now := e.Roles()
	edge := now.Memberships()[3]
	if !edge.Admin || !edge.Inherit || edge.Set || !read.Memberships()[3].Set {
		t.Fatal("omitted options or old read changed", edge)
	}
	roleExecute(t, e, op, "GRANT r TO m")
	if e.RoleStore.Published != nowPublication {
		t.Fatal("no-change grant invalidated authority")
	}
	before := e.RoleStore.Published
	_, err := e.Execute(op, enginetest.Statement(t, "GRANT r TO x,absent"), 0, nil)
	enginetest.State(t, err, "42704")
	if e.RoleStore.Published != before {
		t.Fatal("failed batch published")
	}
	_, err = e.Execute(op, enginetest.Statement(t, "GRANT m TO r WITH INHERIT FALSE, SET FALSE"), 0, nil)
	enginetest.State(t, err, "0LP01")
	if e.RoleStore.Published != before {
		t.Fatal("cycle published")
	}
}

func TestMembershipInitialFailureFences(t *testing.T) {
	e := engine.New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE ROLE p")
	before, allocator := e.RoleStore.Published, e.RoleStore.NextPrincipal()
	for _, q := range []string{"CREATE ROLE n IN ROLE p ROLE p", "CREATE ROLE n ROLE p ADMIN absent", "CREATE ROLE n ROLE p ROLE p"} {
		_, err := e.Execute(op, enginetest.Statement(t, q), 0, nil)
		if err == nil || e.RoleStore.Published != before || e.RoleStore.NextPrincipal() != allocator {
			t.Fatal(q, err, "partial publication")
		}
	}
	roleExecute(t, e, op, "CREATE ROLE n IN ROLE p")
	if len(fixtureEdges(e.RoleStore.Published)) != 4 || !fixtureEdges(e.RoleStore.Published)[3].Inherit || !fixtureEdges(e.RoleStore.Published)[3].Set || fixtureEdges(e.RoleStore.Published)[3].Admin {
		t.Fatal(fixtureEdges(e.RoleStore.Published))
	}
}

func TestMembershipInitialAttributeCutover(t *testing.T) {
	e := engine.New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, admin, "CREATE ROLE parent")
	roleExecute(t, e, admin, "CREATE USER creator CREATEROLE")
	actor, closeActor := roleOperation(t, e, "creator")
	defer closeActor()
	beforePublication := e.RoleStore.Published
	next := e.RoleStore.NextPrincipal()
	_, err := e.Execute(actor, enginetest.Statement(t, "CREATE ROLE failed LOGIN NOINHERIT CONNECTION LIMIT 2 PASSWORD 'secret' ROLE absent"), 0, nil)
	enginetest.State(t, err, "42704")
	if e.RoleStore.Published != beforePublication || e.RoleStore.NextPrincipal() != next {
		t.Fatal("initial membership failure published attributes, creator ADMIN or allocator")
	}
	roleExecute(t, e, admin, "CREATE ROLE child NOINHERIT IN ROLE parent CONNECTION LIMIT 2 PASSWORD NULL VALID UNTIL 'infinity'")
	child, _ := e.Roles().Lookup("child")
	parent, _ := e.Roles().Lookup("parent")
	if child.Inherit || child.ConnectionLimit != 2 || child.Password != "" || !child.HasValidUntil {
		t.Fatal("combined attributes lost", child)
	}
	edge := e.Roles().Memberships()[3]
	if edge.Role != parent.Ref || edge.Member != child.Ref || edge.Inherit || !edge.Set || edge.Admin {
		t.Fatal("initial edge ignored NOINHERIT", edge)
	}
	roleExecute(t, e, admin, "ALTER ROLE child INHERIT")
	if e.Roles().Memberships()[3] != edge {
		t.Fatal("ALTER attribute rewrote old initial membership")
	}
	roleExecute(t, e, actor, "CREATE ROLE owned NOINHERIT ROLE child")
	owned, _ := e.Roles().Lookup("owned")
	creator, _ := e.Roles().Lookup("creator")
	edges := e.Roles().Memberships()
	if len(edges) != 6 || edges[4] != (authorization.Membership{Role: owned.Ref, Member: creator.Ref, Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Admin: true}) || edges[5].Role != owned.Ref || edges[5].Member != child.Ref || !edges[5].Inherit || !edges[5].Set {
		t.Fatal("creator ADMIN and initial membership were not combined", edges)
	}
	roleExecute(t, e, actor, "ALTER ROLE owned LOGIN")
}

func TestMembershipReferenceValidationAndRevision(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE ROLE r")
	enginetest.Run(t, e, "CREATE ROLE m")
	r, _ := e.Roles().Lookup("r")
	m, _ := e.Roles().Lookup("m")
	bootstrap := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	foreign := engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, ref := range []authorization.PrincipalRef{{}, foreign} {
		for _, edge := range []authorization.Membership{{Role: ref, Member: m.Ref, Grantor: bootstrap}, {Role: r.Ref, Member: ref, Grantor: bootstrap}, {Role: r.Ref, Member: m.Ref, Grantor: ref}} {
			before := e.RoleStore.Published
			if err := grantMembership(t, e, edge); err == nil || e.RoleStore.Published != before {
				t.Fatal("invalid ref published", edge, err)
			}
		}
	}
	saturated := *e.RoleStore.Published
	saturated.Revision = ^uint64(0)
	e.RoleStore.PublishLocked(&saturated)
	before := e.RoleStore.Published
	if err := grantMembership(t, e, authorization.Membership{Role: r.Ref, Member: m.Ref, Grantor: bootstrap}); err == nil || e.RoleStore.Published != before || len(fixtureEdges(e.RoleStore.Published)) != 3 {
		t.Fatal("revision exhaustion published", err)
	}
}

func TestMembershipCascadeAlternateSource(t *testing.T) {
	e := engine.New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, name := range []string{"r", "a", "b", "c", "x"} {
		roleExecute(t, e, op, "CREATE ROLE "+name)
	}
	for _, q := range []string{"GRANT r TO a,x WITH ADMIN TRUE", "GRANT r TO b WITH ADMIN TRUE GRANTED BY a", "GRANT r TO c GRANTED BY b", "GRANT r TO a WITH ADMIN TRUE GRANTED BY x"} {
		roleExecute(t, e, op, q)
	}
	old := e.Roles()
	roleExecute(t, e, op, "REVOKE ADMIN OPTION FOR r FROM a")
	if len(fixtureEdges(e.RoleStore.Published)) != 8 || !old.Memberships()[3].Admin {
		t.Fatal("alternate source or old read", fixtureEdges(e.RoleStore.Published))
	}
	before := e.RoleStore.Published
	_, err := e.Execute(op, enginetest.Statement(t, "REVOKE r FROM a GRANTED BY x"), 0, nil)
	enginetest.State(t, err, "2BP01")
	if e.RoleStore.Published != before {
		t.Fatal("restrict published")
	}
	roleExecute(t, e, op, "REVOKE r FROM a GRANTED BY x CASCADE")
	if len(fixtureEdges(e.RoleStore.Published)) != 5 {
		t.Fatal("cascade graph", fixtureEdges(e.RoleStore.Published))
	}
}

func TestMembershipCancelledCandidate(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE ROLE r")
	enginetest.Run(t, e, "CREATE ROLE m")
	op, done := roleOperation(t, e, "postgres")
	defer done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Closure is also used inside planning; cancellation cannot return a prefix.
	old := e.RoleStore.Published
	refs, err := e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()).RoleClosure(ctx, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), false)
	if err != context.Canceled || refs != nil || !reflect.DeepEqual(e.RoleStore.Published, old) {
		t.Fatal(fmt.Sprint(refs), err)
	}
	op.Ctx = ctx
	_, err = e.Execute(op, enginetest.Statement(t, "GRANT r TO m"), 0, nil)
	if err != context.Canceled || e.RoleStore.Published != old || len(fixtureEdges(e.RoleStore.Published)) != 3 {
		t.Fatal("cancelled membership published", err)
	}
}

func TestMembershipConcurrentCyclePublication(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE ROLE a")
	enginetest.Run(t, e, "CREATE ROLE b")
	first, doneFirst := roleOperation(t, e, "postgres")
	defer doneFirst()
	second, doneSecond := roleOperation(t, e, "postgres")
	defer doneSecond()
	statements := []parser.Statement{enginetest.Statement(t, "GRANT a TO b WITH INHERIT FALSE, SET FALSE"), enginetest.Statement(t, "GRANT b TO a WITH INHERIT FALSE, SET FALSE")}
	start := make(chan struct{})
	results := make(chan error, 2)
	for index, op := range []*session.Operation{first, second} {
		go func(op *session.Operation, stmt parser.Statement) {
			<-start
			_, err := e.Execute(op, stmt, 0, nil)
			results <- err
		}(op, statements[index])
	}
	close(start)
	successes, failures := 0, 0
	for range 2 {
		var err error
		select {
		case err = <-results:
		case <-time.After(5 * time.Second):
			t.Fatal("membership workers did not finish")
		}
		if err == nil {
			successes++
		} else {
			enginetest.State(t, err, "0LP01")
			failures++
		}
	}
	if successes != 1 || failures != 1 || len(fixtureEdges(e.RoleStore.Published)) != 4 {
		t.Fatal("concurrent cycle", successes, failures, fixtureEdges(e.RoleStore.Published))
	}
}

func TestMembershipOrderedCandidateFailureFences(t *testing.T) {
	for _, tc := range []struct {
		name        string
		setup       []string
		query, code string
	}{
		{"inferred grantor", []string{"CREATE USER a", "CREATE ROLE b", "CREATE ROLE c", "GRANT b TO a WITH ADMIN TRUE, INHERIT FALSE, SET FALSE", "SET SESSION AUTHORIZATION a", "GRANT b TO a", "RESET SESSION AUTHORIZATION", "GRANT c TO b WITH ADMIN TRUE", "SET SESSION AUTHORIZATION a"}, "GRANT b,c TO a WITH INHERIT FALSE", "XX000"},
		{"sanity before admin", []string{"CREATE ROLE r", "CREATE ROLE a", "GRANT r TO a WITH ADMIN TRUE"}, "GRANT r TO a,r WITH ADMIN TRUE GRANTED BY a", "0LP01"},
		{"sanity before notice", []string{"CREATE ROLE r", "CREATE ROLE a", "GRANT r TO a"}, "GRANT r TO a,r", "0LP01"},
		{"recipient restrict", []string{"CREATE ROLE r", "CREATE ROLE a", "CREATE ROLE b", "CREATE ROLE c", "GRANT r TO a WITH ADMIN TRUE", "GRANT r TO b GRANTED BY a"}, "REVOKE r FROM a,c RESTRICT", "2BP01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, q := range tc.setup {
				roleExecute(t, e, op, q)
			}
			op.DetachNotices()
			retainedPublication := e.RoleStore.Published
			retained := e.Roles()
			edges := retained.Memberships()
			catalog := e.Catalog.State
			_, err := e.Execute(op, enginetest.Statement(t, tc.query), 0, nil)
			enginetest.State(t, err, tc.code)
			if notices := op.DetachNotices(); len(notices) != 0 {
				t.Fatal("unreached mutation notice", notices)
			}
			if e.RoleStore.Published != retainedPublication || e.Catalog.State != catalog || !reflect.DeepEqual(edges, retained.Memberships()) || !reflect.DeepEqual(edges, e.Roles().Memberships()) {
				t.Fatal("failed ordered candidate changed authority", err)
			}
		})
	}
}

func TestMembershipRevokeOriginalRowActions(t *testing.T) {
	e := engine.New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE ROLE r", "CREATE ROLE a", "CREATE ROLE b", "CREATE ROLE c", "GRANT r TO a,b WITH ADMIN TRUE", "GRANT r TO a WITH ADMIN TRUE GRANTED BY b", "GRANT r TO b WITH ADMIN TRUE GRANTED BY a", "GRANT r TO b GRANTED BY b", "GRANT r TO a,b WITH ADMIN FALSE"} {
		roleExecute(t, e, op, q)
	}
	op.DetachNotices()
	retained := e.Roles()
	// ADMIN FALSE updates legitimately leave delegated sources intact. The
	// b-origin self grant is then removed recursively before its explicit turn.
	roleExecute(t, e, op, "REVOKE r FROM a,a,b GRANTED BY b CASCADE")
	if notices := op.DetachNotices(); len(notices) != 0 {
		t.Fatal("original row matches warned", notices)
	}
	edges := e.Roles().Memberships()
	if len(edges) != 5 || len(retained.Memberships()) != 8 {
		t.Fatal(edges, retained.Memberships())
	}
	for _, edge := range edges {
		if edge.Grantor != e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres") || edge.Admin || !edge.Inherit || !edge.Set {
			t.Fatal("independent source changed", edge)
		}
	}
	roleExecute(t, e, op, "REVOKE r FROM c,c GRANTED BY b")
	notices := op.DetachNotices()
	if len(notices) != 2 || notices[0].Code != "01000" || notices[1] != notices[0] {
		t.Fatal("absent duplicates", notices)
	}
}

func TestMembershipRevokeEntryAuthority(t *testing.T) {
	e := engine.New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	// Same graph as the pinned public BatchRevokeAuthority witness. An
	// implicit actor grantor would select no p row and prove nothing.
	for _, q := range []string{
		"CREATE USER a", "CREATE ROLE p", "CREATE ROLE q", "CREATE ROLE g",
		"GRANT p TO g WITH ADMIN TRUE",
		"GRANT p TO a WITH ADMIN TRUE GRANTED BY g",
		"GRANT g TO a", "GRANT q TO p WITH ADMIN TRUE",
		"GRANT p TO g WITH ADMIN FALSE", "REVOKE p FROM g",
		"SET SESSION AUTHORIZATION a",
	} {
		roleExecute(t, e, op, q)
	}
	op.DetachNotices()
	retainedPublication := e.RoleStore.Published
	retained := e.Roles()
	original := retained.Memberships()
	catalog := e.Catalog.State
	roleExecute(t, e, op, "REVOKE p,q FROM a GRANTED BY g")
	notices := op.DetachNotices()
	if len(notices) != 1 || notices[0].Code != "01000" || notices[0].Message != `role "a" has not been granted membership in role "q" by role "g"` {
		t.Fatal("entry authority warning", notices)
	}
	want := make([]authorization.Membership, 0, len(original)-1)
	for _, edge := range original {
		if edge.Role == retained.LookupRef("p") && edge.Member == retained.LookupRef("a") && edge.Grantor == retained.LookupRef("g") {
			continue
		}
		want = append(want, edge)
	}
	if len(want) != len(original)-1 || !reflect.DeepEqual(want, e.Roles().Memberships()) || !reflect.DeepEqual(original, retained.Memberships()) || e.RoleStore.Published.Revision != retainedPublication.Revision+1 || e.Catalog.State != catalog {
		t.Fatal("entry batch publication", original, e.Roles().Memberships())
	}
	published := e.RoleStore.Published
	_, err := e.Execute(op, enginetest.Statement(t, "REVOKE q FROM a GRANTED BY g"), 0, nil)
	enginetest.State(t, err, "42501")
	if e.RoleStore.Published != published || len(op.DetachNotices()) != 0 {
		t.Fatal("next statement used obsolete authority", err)
	}
}

func TestMembershipRevokeTargetActionOverlap(t *testing.T) {
	for _, tc := range []struct {
		name, grant, query, code string
		warnings                 int
	}{
		{"delete", "GRANT r TO a", "REVOKE r,r FROM a", "XX000", 0},
		{"clear admin", "GRANT r TO a WITH ADMIN TRUE", "REVOKE ADMIN OPTION FOR r,r FROM a", "XX000", 0},
		{"clear inherit", "GRANT r TO a", "REVOKE INHERIT OPTION FOR r,r FROM a", "XX000", 0},
		{"clear set", "GRANT r TO a", "REVOKE SET OPTION FOR r,r FROM a", "XX000", 0},
		// REL18 assigns I/S update actions even for an already-false flag;
		// ADMIN FALSE is the genuine found-row no-op.
		{"false inherit update", "GRANT r TO a WITH INHERIT FALSE", "REVOKE INHERIT OPTION FOR r,r FROM a", "XX000", 0},
		{"false set update", "GRANT r TO a WITH SET FALSE", "REVOKE SET OPTION FOR r,r FROM a", "XX000", 0},
		{"false admin noop", "GRANT r TO a", "REVOKE ADMIN OPTION FOR r,r FROM a", "", 0},
		{"absent original", "GRANT r TO a", "REVOKE r,r FROM b", "", 2},
		{"same target duplicate member", "GRANT r TO a", "REVOKE r FROM a,a", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE ROLE r", "CREATE ROLE a", "CREATE ROLE b", tc.grant} {
				roleExecute(t, e, op, q)
			}
			op.DetachNotices()
			beforePublication := e.RoleStore.Published
			before := e.Roles()
			original := before.Memberships()
			_, err := e.Execute(op, enginetest.Statement(t, tc.query), 0, nil)
			if tc.code != "" {
				enginetest.State(t, err, tc.code)
				if err.Error() != "stuntgres: tuple already updated by self" || e.RoleStore.Published != beforePublication {
					t.Fatal("overlap publication or diagnostic", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			notices := op.DetachNotices()
			if len(notices) != tc.warnings {
				t.Fatal("original matching warnings", notices)
			}
			for _, notice := range notices {
				if notice.Code != "01000" || notice.Message != `role "b" has not been granted membership in role "r" by role "postgres"` {
					t.Fatal(notice)
				}
			}
			if !reflect.DeepEqual(original, before.Memberships()) {
				t.Fatal("retained graph changed")
			}
			if tc.code == "" && tc.name != "same target duplicate member" && e.RoleStore.Published != beforePublication {
				t.Fatal("noop invalidated authority")
			}
			if tc.name == "same target duplicate member" && len(fixtureEdges(e.RoleStore.Published)) != 3 {
				t.Fatal("duplicate member failed to delete", fixtureEdges(e.RoleStore.Published))
			}
		})
	}
}
