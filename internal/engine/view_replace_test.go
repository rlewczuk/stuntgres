package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"sync"
	"testing"
)

func viewReplaceError(t *testing.T, err error, want sqlstate.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

func TestViewReplaceIdentityAndACL(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER reader", "CREATE USER successor", "CREATE TABLE t(id INT,txt TEXT)", "CREATE TABLE u(id INT,txt TEXT)", "INSERT INTO t VALUES(1,'a')", "INSERT INTO u VALUES(2,'b')", "CREATE VIEW v AS SELECT id,txt FROM t", "CREATE VIEW w AS SELECT id FROM v"} {
		roleExecute(t, e, op, q)
	}
	ref := roleTableRef(t, e, "v")
	reader, _ := e.Roles().Lookup("reader")
	successor, _ := e.Roles().Lookup("successor")
	grantor := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
	if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
	old := e.state.descriptors[ref.Address].(*relationDescriptor)
	copyDescriptor := *old
	copyDescriptor.columnACL = map[int32]authorization.ACLValue{old.attributes[0]: acl, old.attributes[1]: {Explicit: true}}
	copyDescriptor.comment = "retained view"
	copyDescriptor.columnComments = map[int32]string{old.attributes[0]: "retained column"}
	copyDescriptor.revision++
	alterSchemaPublishDescriptor(t, e, &copyDescriptor)
	old = e.state.descriptors[ref.Address].(*relationDescriptor)
	roleExecute(t, e, op, "CREATE OR REPLACE VIEW v WITH(security_barrier=true,security_invoker=true) AS SELECT id+10 AS id,txt,TRUE AS extra FROM u")
	next := e.state.descriptors[ref.Address].(*relationDescriptor)
	if next == old || next.view == old.view || next.address != old.address || next.owner != old.owner || next.view.rule != old.view.rule || !reflect.DeepEqual(next.attributes[:2], old.attributes) || !reflect.DeepEqual(next.acl, old.acl) || !reflect.DeepEqual(next.columnACL, old.columnACL) || next.comment != old.comment || !reflect.DeepEqual(next.columnComments, old.columnComments) {
		t.Fatal("replacement lost immutable identity/ACL/comment", old, next)
	}
	if len(old.columns) != 2 || len(next.columns) != 3 || next.attributes[2] == old.attributes[0] || next.attributes[2] == old.attributes[1] {
		t.Fatal(old.columns, next.columns, next.attributes)
	}
	if got := tuples(run(t, e, "SELECT id FROM w")); !reflect.DeepEqual(got, [][]driver.Value{{int64(12)}}) {
		t.Fatal(got)
	}
	roleExecute(t, e, op, "DROP TABLE t")
	_, err := testExecute(e, context.Background(), statement(t, "DROP TABLE u"), nil)
	viewReplaceError(t, err, sqlstate.Error{Code: "2BP01", Message: "cannot drop table u because other objects depend on it", Detail: "view v depends on table u\nview w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	roleExecute(t, e, op, "ALTER VIEW v SET(security_invoker=false)")
	flags := e.state.descriptors[ref.Address].(*relationDescriptor).view
	if !flags.barrier || flags.invoker {
		t.Fatal(flags)
	}
	roleExecute(t, e, op, "ALTER VIEW v RESET(security_barrier,unknown_option,unknown_option,check_option)")
	flags = e.state.descriptors[ref.Address].(*relationDescriptor).view
	if flags.barrier || flags.invoker {
		t.Fatal(flags)
	}
	roleExecute(t, e, op, "ALTER VIEW v SET(security_barrier=true,security_invoker=true)")
	roleExecute(t, e, op, "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,txt,TRUE AS extra FROM u")
	flags = e.state.descriptors[ref.Address].(*relationDescriptor).view
	if flags.barrier || flags.invoker {
		t.Fatal("replace did not clear options", flags)
	}
	roleExecute(t, e, op, "ALTER VIEW v OWNER TO successor")
	transferred := e.state.descriptors[ref.Address].(*relationDescriptor)
	wantACL := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: successor.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
	if transferred.owner != successor.Ref || !reflect.DeepEqual(transferred.acl, wantACL) || !reflect.DeepEqual(transferred.columnACL[old.attributes[0]], wantACL) || !reflect.DeepEqual(transferred.columnACL[old.attributes[1]], authorization.ACLValue{Explicit: true}) {
		t.Fatal("owner ACL rewrite", transferred)
	}
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err == nil {
		t.Fatal("explicit ACL dump silently discarded grants")
	}
}

func TestViewReplaceOwnerMembership(t *testing.T) {
	for _, tc := range []struct {
		name         string
		inherit, set bool
		allowed      bool
	}{{"inherit", true, false, true}, {"set_only", false, true, false}, {"disabled", false, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE USER owner", "CREATE USER middle", "CREATE USER member", "CREATE SCHEMA s AUTHORIZATION owner", "SET SESSION AUTHORIZATION owner", "CREATE TABLE s.t(id INT)", "CREATE VIEW s.v AS SELECT id FROM s.t", "RESET SESSION AUTHORIZATION"} {
				roleExecute(t, e, op, q)
			}
			owner, _ := e.Roles().Lookup("owner")
			middle, _ := e.Roles().Lookup("middle")
			member, _ := e.Roles().Lookup("member")
			grantor := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
			for _, m := range []authorization.Membership{{Role: owner.Ref, Member: middle.Ref, Grantor: grantor, Inherit: tc.inherit, Set: tc.set}, {Role: middle.Ref, Member: member.Ref, Grantor: grantor, Inherit: tc.inherit, Set: tc.set}, {Role: owner.Ref, Member: member.Ref, Grantor: grantor, Inherit: tc.inherit, Set: tc.set}} {
				if err := grantMembership(t, e, m); err != nil {
					t.Fatal(err)
				}
			}
			// Namespace rights isolate view ownership, not inherited schema ACLs.
			nsAddress := e.CatalogRead().Lookup(NameKey{ClassID: NamespaceClassID, Name: "s"})[0]
			ns := ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: nsAddress}
			if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: member.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}); err != nil {
				t.Fatal(err)
			}
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION member")
			for _, q := range []string{"CREATE OR REPLACE VIEW s.v AS SELECT id FROM s.t", "ALTER VIEW s.v SET(security_barrier=true)", "DROP VIEW s.v"} {
				before := e.state
				result, err := e.Execute(op, statement(t, q), 0, nil)
				if tc.allowed {
					if err != nil || result.Count != 0 || result.Rows != nil {
						t.Fatal(q, result, err)
					}
				} else {
					viewReplaceError(t, err, sqlstate.Error{Code: "42501", Message: "must be owner of view v"})
					if e.state != before {
						t.Fatal("denied operation published")
					}
				}
			}
		})
	}
}

func TestViewReplaceAtomicCancellation(t *testing.T) {
	for _, tc := range []struct{ name, target, query string }{
		{"replacement_target", "v", "CREATE OR REPLACE VIEW v AS SELECT id,TRUE AS extra FROM u"},
		{"replacement_source", "u", "CREATE OR REPLACE VIEW v AS SELECT id,TRUE AS extra FROM u"},
		{"drop_target", "v", "DROP VIEW v CASCADE"},
		{"drop_dependent", "w", "DROP VIEW v CASCADE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			writer := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE TABLE u(id INT)", "INSERT INTO t VALUES(1)", "CREATE VIEW v AS SELECT id FROM t", "CREATE VIEW w AS SELECT id FROM v"} {
				sessionSQL(t, holder, q)
			}
			before := e.CatalogRead().state
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "LOCK TABLE "+tc.target+" IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pending := lockAsync(e, writer, ctx, tc.query, false)
			lockPending(t, e, 1)
			cancel()
			lockAwait(t, pending, context.Canceled)
			lockPending(t, e, 0)
			transactionEnd(t, holder, tx, false)
			if e.CatalogRead().state != before {
				t.Fatal("canceled utility published", tc.query)
			}
			lockEmpty(t, e)
			if got := tuples(sessionSQL(t, writer, "SELECT id FROM w")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}}) {
				t.Fatal(got)
			}
		})
	}
	// Descriptor replacement validates through the provider hook. Removal-only
	// DROP stages instead use the real lock cancellation witnesses above.
	for _, q := range []string{"CREATE OR REPLACE VIEW v AS SELECT id,TRUE AS extra FROM t", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW v SET(security_invoker=true)"} {
		t.Run(q, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(id INT)")
			run(t, e, "CREATE VIEW v AS SELECT id FROM t")
			before := e.CatalogRead().state
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := e.providers[RelationClassID]
			var once sync.Once
			e.providers[RelationClassID] = alterDropProvider{CatalogProvider: original, callback: func() { once.Do(func() { e.providers[RelationClassID] = original; cancel() }) }}
			_, err := testExecute(e, ctx, statement(t, q), nil)
			if !errors.Is(err, context.Canceled) || e.CatalogRead().state != before {
				t.Fatal(err, "partial publication")
			}
			lockEmpty(t, e)
		})
	}
}

func TestViewReplaceCycles(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t", "CREATE OR REPLACE VIEW v AS SELECT id FROM v"} {
		run(t, e, q)
	}
	for _, q := range []string{"SELECT id FROM v", "SELECT id FROM v FOR UPDATE"} {
		_, err := testExecute(e, context.Background(), statement(t, q), nil)
		viewReplaceError(t, err, sqlstate.Error{Code: "42P17", Message: "infinite recursion detected in rules for relation \"v\""})
		lockEmpty(t, e)
	}
	locker := sessionStart(t, e, nil)
	tx := transactionBegin(t, locker)
	sessionSQL(t, locker, "LOCK TABLE v IN ACCESS SHARE MODE")
	transactionEnd(t, locker, tx, false)
	lockEmpty(t, e)
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err == nil || err.Error() != "stuntgres: cyclic view dump dependencies" {
		t.Fatal(err)
	}
	if dump.Len() != 0 {
		t.Fatal("self-referential dump emitted partial output")
	}
	run(t, e, "CREATE OR REPLACE VIEW v AS SELECT id FROM t")
	if got := tuples(run(t, e, "SELECT a.id,b.id FROM v a JOIN v b ON a.id=b.id")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7), int64(7)}}) {
		t.Fatal(got)
	}
	run(t, e, "CREATE VIEW w AS SELECT id FROM v")
	run(t, e, "CREATE OR REPLACE VIEW v AS SELECT id FROM w")
	dump.Reset()
	if err := e.Dump(context.Background(), &dump); err == nil || err.Error() != "stuntgres: cyclic view dump dependencies" {
		t.Fatal(err)
	}
	if dump.Len() != 0 {
		t.Fatal("mutually recursive dump emitted partial output")
	}
	run(t, e, "DROP VIEW v CASCADE")
	lockEmpty(t, e)
}
