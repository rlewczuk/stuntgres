package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func sequencePreparedDescription(t *testing.T, e *Engine, s *session.Session, query string) (parser.Statement, StatementDescription) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ast := statement(t, query)
	d, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	return ast, d
}
func sequencePreparedExecute(e *Engine, s *session.Session, ast parser.Statement, d *StatementDescription) (Result, error) {
	op, err := s.Acquire(context.Background())
	if err != nil {
		return Result{}, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return Result{}, err
	}
	scope.Expected = d
	r, err := scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	return r, err
}
func TestSequencePreparedAuthority(t *testing.T) {
	t.Run("ACLInvalidation", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
			sessionSQL(t, s, q)
		}
		ast, d := sequencePreparedDescription(t, e, s, "SELECT nextval('s')")
		original := d.Sequences.Constants[0].Ref
		oldStamp := e.Catalog.State.MustRelation(original.Address).SequencePlanStamp
		sessionSQL(t, s, "CREATE SEQUENCE a.s START 100")
		if err := setObjectACL(e, context.Background(), original, authorization.ACLValue{}); err != nil {
			t.Fatal(err)
		}
		if e.Catalog.State.MustRelation(original.Address).SequencePlanStamp == oldStamp {
			t.Fatal("ACL publication did not invalidate relation plan stamp")
		}
		r, err := sequencePreparedExecute(e, s, ast, &d)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Rows.Close()
		if got := tuples(r); got[0][0] != int64(100) {
			t.Fatal("ACL trigger retained old identity", got)
		}
	})
	t.Run("StablePayloadAndEnvironment", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
			sessionSQL(t, s, q)
		}
		ast, d := sequencePreparedDescription(t, e, s, "SELECT nextval('s')")
		cell := d.Sequences
		constant := &cell.Constants[0]
		path := &cell.Path.Namespaces[0]
		key, _ := PayloadOf(s).Sequences.CachedPathKey()
		ref := cell.Constants[0].Ref
		stamp := e.Catalog.State.MustRelation(ref.Address).SequencePlanStamp
		sessionSQL(t, s, "CREATE SEQUENCE a.s START 100")
		for _, want := range []int64{10, 11, 12} {
			r, err := sequencePreparedExecute(e, s, ast, &d)
			if err != nil {
				t.Fatal(err)
			}
			got := tuples(r)
			r.Rows.Close()
			if got[0][0] != want {
				t.Fatal(got, want)
			}
			if d.Sequences != cell || &cell.Constants[0] != constant || &cell.Path.Namespaces[0] != path || !cachedSequencePathIs(s, key) {
				t.Fatal("unchanged execution replaced analysis or effective namespace cache")
			}
		}
		for _, q := range []string{"ALTER SEQUENCE b.s OWNED BY NONE", "ALTER SEQUENCE b.s OWNER TO postgres", "ALTER SEQUENCE b.s SET LOGGED"} {
			sessionSQL(t, s, q)
			if e.Catalog.State.MustRelation(ref.Address).SequencePlanStamp != stamp {
				t.Fatal("source-grounded no-op invalidated", q)
			}
		}
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			op.Done()
			t.Fatal(err)
		}
		if valid, err := EngineSequenceAnalysisValidLocked(e, auth, d.Sequences); err != nil || !valid {
			e.Mu.Unlock()
			op.Done()
			t.Fatal(valid, err)
		}
		allocations := testing.AllocsPerRun(100, func() {
			if valid, err := EngineSequenceAnalysisValidLocked(e, auth, d.Sequences); err != nil || !valid {
				panic("unchanged analysis became invalid")
			}
		})
		e.Mu.Unlock()
		op.Done()
		if allocations != 0 {
			t.Fatal("unchanged sequence cache validation allocated", allocations)
		}
		sessionSQL(t, s, "ALTER SEQUENCE b.s START 10")
		if e.Catalog.State.MustRelation(ref.Address).SequencePlanStamp == stamp {
			t.Fatal("unchanged START failed to invalidate")
		}
		sequenceScalar(t, s, "SELECT nextval('b.s')", 13)
	})
	t.Run("QueuedRename", func(t *testing.T) {
		e := New()
		a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE SEQUENCE s START 10")
		sequenceScalar(t, a, "SELECT nextval('s')", 10)
		ast, d := sequencePreparedDescription(t, e, b, "SELECT nextval('s')")
		tx := transactionBegin(t, a)
		sessionSQL(t, a, "ALTER SEQUENCE s RENAME TO identity")
		done := make(chan error, 1)
		go func() {
			r, err := sequencePreparedExecute(e, b, ast, &d)
			if err == nil {
				got := tuples(r)
				r.Rows.Close()
				if got[0][0] != int64(11) {
					err = fmt.Errorf("in-flight renamed OID got %v want11", got)
				}
			}
			done <- err
		}()
		lockPending(t, e, 1)
		transactionEnd(t, a, tx, true)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		_, err := sequencePreparedExecute(e, b, ast, &d)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "s" does not exist`, Position: "16"}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
			t.Fatalf("next execution error=%#v want=%#v", diagnostic, want)
		}
		sequenceScalar(t, a, "SELECT nextval('identity')", 12)
		lockEmpty(t, e)
	})
	t.Run("QueuedRollback", func(t *testing.T) {
		e := New()
		a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE SEQUENCE s START 14")
		ast, d := sequencePreparedDescription(t, e, b, "SELECT nextval('s')")
		tx := transactionBegin(t, a)
		sessionSQL(t, a, "ALTER SEQUENCE s RENAME TO identity")
		done := make(chan error, 1)
		go func() {
			r, err := sequencePreparedExecute(e, b, ast, &d)
			if err == nil {
				got := tuples(r)
				r.Rows.Close()
				if got[0][0] != int64(14) {
					err = errors.New("rollback waiter did not allocate original14")
				}
			}
			done <- err
		}()
		lockPending(t, e, 1)
		transactionEnd(t, a, tx, false)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		lockEmpty(t, e)
	})
	t.Run("QueuedDrop", func(t *testing.T) {
		e := New()
		a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE SEQUENCE s START 14")
		ast, d := sequencePreparedDescription(t, e, b, "SELECT nextval('s')")
		tx := transactionBegin(t, a)
		sessionSQL(t, a, "DROP SEQUENCE s")
		done := make(chan error, 1)
		go func() {
			r, err := sequencePreparedExecute(e, b, ast, &d)
			if r.Rows != nil {
				r.Rows.Close()
			}
			done <- err
		}()
		lockPending(t, e, 1)
		transactionEnd(t, a, tx, true)
		var diagnostic *sqlstate.Error
		err := <-done
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: fmt.Sprintf("could not open relation with OID %d", d.Sequences.Constants[0].Ref.Address.ObjectID)}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatalf("queued error=%#v want=%#v", diagnostic, want)
		}
		_, err = sequencePreparedExecute(e, b, ast, &d)
		want = sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "s" does not exist`, Position: "16"}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatalf("next execution error=%#v want=%#v", diagnostic, want)
		}
		lockEmpty(t, e)
	})
}

func TestSequencePreparedEffectiveAuthority(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE USER reader", "CREATE USER reader2", "CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SCHEMA reader", "CREATE SCHEMA reader2", "CREATE SEQUENCE a.s START 100", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE reader.s START 30", "CREATE SEQUENCE reader2.s START 300"} {
		sessionSQL(t, s, q)
	}
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(name string, rights authorization.Privileges) {
		t.Helper()
		var ref ids.ObjectRef
		if a, ok := e.Catalog.State.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name}); ok {
			ref = e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), a)
		} else {
			ref = roleTableRef(t, e, name)
		}
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: rights}}}
		if err := setObjectACL(e, context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a", "b", "reader", "reader2"} {
		grant(name, authorization.UsagePrivilege|authorization.CreatePrivilege)
		grant(name+".s", authorization.UsagePrivilege)
	}
	grant("a", 0)
	sessionSQL(t, s, "SET SESSION AUTHORIZATION reader")
	sessionSQL(t, s, "SET search_path TO a,b")
	ast, d := sequencePreparedDescription(t, e, s, "SELECT nextval('s')")
	qualified, qd := sequencePreparedDescription(t, e, s, "SELECT nextval('b.s')")
	result := func(ast parser.Statement, d *StatementDescription, want int64) {
		t.Helper()
		r, err := sequencePreparedExecute(e, s, ast, d)
		if err != nil {
			t.Fatal(err)
		}
		got := tuples(r)
		r.Rows.Close()
		if got[0][0] != want {
			t.Fatal(got, want)
		}
	}
	grant("a", authorization.UsagePrivilege)
	result(ast, &d, 100)
	grant("b", 0)
	_, err := sequencePreparedExecute(e, s, qualified, &qd)
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema b", Position: "16"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatalf("schema reanalysis got=%#v want=%#v", diagnostic, want)
	}
	sessionSQL(t, s, `SET search_path TO "$user",b`)
	user, ud := sequencePreparedDescription(t, e, s, "SELECT nextval('s')")
	result(user, &ud, 30)
	sessionSQL(t, s, "RESET SESSION AUTHORIZATION")
	sessionSQL(t, s, "SET SESSION AUTHORIZATION reader2")
	result(user, &ud, 300)
	sessionSQL(t, s, "RESET SESSION AUTHORIZATION")
	sessionSQL(t, s, "SET search_path TO a,b")
	// A real owner change invalidates; same-owner and same-persistence do not.
	grant("b", authorization.UsagePrivilege|authorization.CreatePrivilege)
	sessionSQL(t, s, "DROP SEQUENCE a.s")
	shadow, sd := sequencePreparedDescription(t, e, s, "SELECT nextval('s')")
	sessionSQL(t, s, "CREATE SEQUENCE a.s START 500")
	owned, od := sequencePreparedDescription(t, e, s, "SELECT nextval('b.s')")
	stamp := od.Sequences.Constants[0].Stamp
	sessionSQL(t, s, "ALTER SEQUENCE b.s OWNER TO reader")
	result(shadow, &sd, 500)
	if e.Catalog.State.MustRelation(od.Sequences.Constants[0].Ref.Address).SequencePlanStamp == stamp {
		t.Fatal("real owner change retained plan stamp")
	}
	result(owned, &od, 10)
	sessionSQL(t, s, "ALTER SEQUENCE b.s SET UNLOGGED")
	stamp = e.Catalog.State.MustRelation(od.Sequences.Constants[0].Ref.Address).SequencePlanStamp
	sessionSQL(t, s, "ALTER SEQUENCE b.s SET UNLOGGED")
	if e.Catalog.State.MustRelation(od.Sequences.Constants[0].Ref.Address).SequencePlanStamp != stamp {
		t.Fatal("same unlogged persistence invalidated")
	}
	sessionSQL(t, s, "ALTER SEQUENCE b.s SET LOGGED")
	if e.Catalog.State.MustRelation(od.Sequences.Constants[0].Ref.Address).SequencePlanStamp == stamp {
		t.Fatal("real logged persistence transition did not invalidate")
	}
}

func TestSequencePreparedTemporaryPersistenceRejected(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TEMP SEQUENCE s")
	ast, d := sequencePreparedDescription(t, e, s, "SELECT nextval('s')")
	ref := d.Sequences.Constants[0].Ref
	descriptor := e.Catalog.State.MustRelation(ref.Address)
	namespace := e.Catalog.State.MustNamespace(descriptor.Namespace())
	for _, action := range []string{"LOGGED", "UNLOGGED"} {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Execute(op, statement(t, "ALTER SEQUENCE s SET "+action), 0, nil)
		op.Done()
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P16", Message: `cannot change logged status of table "s" because it is temporary`, Schema: namespace.Name(), Table: "s"}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatalf("got=%#v want=%#v", diagnostic, want)
		}
		if e.Catalog.State.Get(ref.Address) != descriptor {
			t.Fatal("rejected persistence published descriptor/stamp")
		}
	}
	r, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	got := tuples(r)
	r.Rows.Close()
	if got[0][0] != int64(1) {
		t.Fatal(got)
	}
}

func TestSequencePreparedRevokedMemory(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE USER reader")
	sessionSQL(t, s, "CREATE SEQUENCE s CACHE 10")
	ref := roleTableRef(t, e, "s")
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	reader := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("reader")
	grant := func(rights authorization.Privileges) {
		t.Helper()
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: rights}}}
		if err := setObjectACL(e, context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
	}
	grant(authorization.UsagePrivilege | authorization.UpdatePrivilege | authorization.SelectPrivilege)
	sessionSQL(t, s, "SET SESSION AUTHORIZATION reader")
	sequenceScalar(t, s, "SELECT nextval('s')", 1)
	queries := []string{"SELECT nextval('s')", "SELECT currval('s')", "SELECT lastval()", "SELECT setval('s',40)"}
	asts := make([]parser.Statement, len(queries))
	descriptions := make([]StatementDescription, len(queries))
	for i, q := range queries {
		asts[i], descriptions[i] = sequencePreparedDescription(t, e, s, q)
	}
	grant(0)
	for i, q := range queries {
		r, err := sequencePreparedExecute(e, s, asts[i], &descriptions[i])
		if r.Rows != nil {
			r.Rows.Close()
			t.Fatal("denied call returned cursor", q)
		}
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for sequence s"}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatalf("%s got=%#v want=%#v", q, diagnostic, want)
		}
	}
	grant(authorization.UsagePrivilege)
	r, err := sequencePreparedExecute(e, s, asts[0], &descriptions[0])
	if err != nil {
		t.Fatal(err)
	}
	got := tuples(r)
	r.Rows.Close()
	if got[0][0] != int64(2) {
		t.Fatal("denied call changed cache/generator", got)
	}
	sequenceScalar(t, s, "SELECT currval('s')", 2)
	sequenceScalar(t, s, "SELECT lastval()", 2)
}

func TestSequencePreparedReplacementProvenance(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE SEQUENCE persistent")
	sessionSQL(t, a, "CREATE TEMP SEQUENCE temporary CACHE 10")
	temporary, td := sequencePreparedDescription(t, e, a, "SELECT nextval('pg_temp.temporary')")
	persistent, pd := sequencePreparedDescription(t, e, a, "SELECT nextval('persistent')")
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		s           *session.Session
		ast         parser.Statement
		description *StatementDescription
	}{{b, temporary, &td}, {a, persistent, &pd}} {
		r, err := sequencePreparedExecute(e, tc.s, tc.ast, tc.description)
		if r.Rows != nil {
			r.Rows.Close()
			t.Fatal("fenced authority returned cursor")
		}
		if !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("replacement provenance was rescued", err)
		}
	}
	r, err := sequencePreparedExecute(e, a, temporary, &td)
	if err != nil {
		t.Fatal(err)
	}
	got := tuples(r)
	r.Rows.Close()
	if got[0][0] != int64(1) {
		t.Fatal(got)
	}
}

func TestSequencePreparedACLPublicationFailure(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE SEQUENCE s")
	_, d := sequencePreparedDescription(t, e, s, "SELECT nextval('s')")
	ref := d.Sequences.Constants[0].Ref
	original := e.Catalog.State
	roles := e.RoleStore.Published
	frontier := e.Catalog.NextRevision()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := setObjectACL(e, ctx, ref, authorization.ACLValue{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.Catalog.State != original || e.RoleStore.Published != roles || e.Catalog.NextRevision() != frontier {
		t.Fatal("canceled ACL update published authority")
	}
	e.Catalog.InstallRevisionFrontierLocked(ids.DefinitionRevision(^uint64(0)))
	if err := setObjectACL(e, context.Background(), ref, authorization.ACLValue{}); err == nil {
		t.Fatal("exhausted stamp allocator accepted ACL publication")
	}
	if e.Catalog.State != original || e.RoleStore.Published != roles {
		t.Fatal("exhausted ACL update published descriptor/stamp/roles")
	}
}

func TestSequencePreparedNamespaceExhaustion(t *testing.T) {
	t.Run("ACL", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE SCHEMA a")
		ref := schemaRef(t, e, "a")
		// Namespace ACLs do not need a new descriptor definition token.
		e.Catalog.InstallRevisionFrontierLocked(ids.DefinitionRevision(^uint64(0)))
		if err := setObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
			t.Fatal("descriptor exhaustion rejected namespace ACL", err)
		}
		// Fail the independent namespace authority without publishing ACL/roles.
		state := catalog.CloneState(e.Catalog.State)
		state.SetNamespaceGeneration(^uint64(0))
		e.Catalog.PublishLocked(state)
		roles := e.RoleStore.Published
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := setObjectACL(e, ctx, ref, authorization.ACLValue{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		err := setObjectACL(e, context.Background(), ref, authorization.ACLValue{})
		if err == nil || err.Error() != "stuntgres: namespace invalidation generation exhausted" {
			t.Fatal("namespace exhaustion accepted ACL publication", err)
		}
		if e.Catalog.State != state || e.RoleStore.Published != roles {
			t.Fatal("failed namespace ACL published catalog or authorization")
		}
		d, _ := e.CatalogRead().Descriptor(ref.Address)
		_, acl, _ := access.DescriptorACL(d)
		if !acl.Explicit {
			t.Fatal("failed ACL update removed the live ACL")
		}
	})
	t.Run("DropClosure", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		for _, query := range []string{"CREATE SCHEMA a", "CREATE TABLE a.t(v INT)", "INSERT INTO a.t VALUES(7)", "CREATE SEQUENCE a.s START 10"} {
			sessionSQL(t, s, query)
		}
		ast, description := sequencePreparedDescription(t, e, s, "SELECT nextval('a.s')")
		state := catalog.CloneState(e.Catalog.State)
		state.SetNamespaceGeneration(^uint64(0))
		e.Catalog.PublishLocked(state)
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Execute(op, statement(t, "DROP SCHEMA a CASCADE"), 0, nil)
		op.Done()
		if err == nil || err.Error() != "stuntgres: namespace invalidation generation exhausted" {
			t.Fatal("namespace exhaustion accepted schema closure", err)
		}
		if e.Catalog.State != state {
			t.Fatal("failed schema closure published partial removals")
		}
		r, err := sequencePreparedExecute(e, s, ast, &description)
		if err != nil {
			t.Fatal("failed closure invalidated retained sequence", err)
		}
		got := tuples(r)
		r.Rows.Close()
		if len(got) != 1 || got[0][0] != int64(10) {
			t.Fatal("failed closure changed sequence state", got)
		}
		sequenceScalar(t, s, "SELECT v FROM a.t", 7)
	})
}

func TestSequencePreparedUndemandedLock(t *testing.T) {
	for _, query := range []string{"SELECT nextval('s') FROM anchor", "SELECT nextval('s') FROM anchor WHERE v=2", "SELECT nextval(NULL)", "SELECT setval('s',NULL)", "SELECT setval('s',42,NULL)", "SELECT setval('s',nextval('t'),NULL)", `SELECT setval('s',nextval('t'),"flag") FROM runtime_null`} {
		t.Run(query, func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE anchor(v INT)", `CREATE TABLE runtime_null("flag" BOOL)`, "INSERT INTO runtime_null VALUES(NULL)", "CREATE SEQUENCE s START 10", "CREATE SEQUENCE t START 20"} {
				sessionSQL(t, a, q)
			}
			if query == "SELECT nextval('s') FROM anchor WHERE v=2" {
				sessionSQL(t, a, "INSERT INTO anchor VALUES(1)")
			}
			if query == `SELECT setval('s',nextval('t'),"flag") FROM runtime_null` {
				sequenceScalar(t, a, "SELECT nextval('t')", 20)
			}
			ast, d := sequencePreparedDescription(t, e, b, query)
			tx := transactionBegin(t, b)
			r, err := sequencePreparedExecute(e, b, ast, &d)
			if err != nil {
				t.Fatal(err)
			}
			got := tuples(r)
			r.Rows.Close()
			if query == "SELECT nextval('s') FROM anchor" || query == "SELECT nextval('s') FROM anchor WHERE v=2" {
				if len(got) != 0 {
					t.Fatal(got)
				}
			} else if len(got) != 1 || got[0][0] != nil {
				t.Fatal("strict NULL result", got)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			op, err := a.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Execute(op, statement(t, "ALTER SEQUENCE s CACHE 1"), 0, nil)
			op.Done()
			if err != nil {
				t.Fatal("undemanded target retained sequence RX and blocked peer ALTER", err)
			}
			transactionEnd(t, b, tx, false)
			sequenceScalar(t, a, "SELECT nextval('s')", 10)
			if query == `SELECT setval('s',nextval('t'),"flag") FROM runtime_null` {
				sequenceScalar(t, b, "SELECT currval('t')", 21)
				sequenceScalar(t, a, "SELECT nextval('t')", 22)
			} else {
				op, err := b.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				_, err = e.Execute(op, statement(t, "SELECT currval('t')"), 0, nil)
				op.Done()
				var diagnostic *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: `currval of sequence "t" is not yet defined in this session`}
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatalf("eliminated argument got=%#v want=%#v", diagnostic, want)
				}
				sequenceScalar(t, a, "SELECT nextval('t')", 20)
			}
		})
	}
}

func TestSequencePreparedQueuedArguments(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE SEQUENCE s START 10")
	sessionSQL(t, a, "CREATE SEQUENCE t START 20")
	ast, d := sequencePreparedDescription(t, e, b, "SELECT setval('s',nextval('t'))")
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "ALTER SEQUENCE s RENAME TO identity")
	done := make(chan error, 1)
	go func() {
		r, err := sequencePreparedExecute(e, b, ast, &d)
		if err == nil {
			got := tuples(r)
			r.Rows.Close()
			if got[0][0] != int64(20) {
				err = fmt.Errorf("in-flight setval got %v want20", got)
			}
		}
		done <- err
	}()
	lockPending(t, e, 1)
	transactionEnd(t, a, tx, true)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	sequenceScalar(t, b, "SELECT currval('t')", 20)
	sequenceScalar(t, b, "SELECT lastval()", 20)
	_, err := sequencePreparedExecute(e, b, ast, &d)
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "s" does not exist`, Position: "15"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatalf("next execute got=%#v want=%#v", diagnostic, want)
	}
	sequenceScalar(t, a, "SELECT nextval('identity')", 21)
	sequenceScalar(t, a, "SELECT nextval('t')", 21)
	lockEmpty(t, e)
}
