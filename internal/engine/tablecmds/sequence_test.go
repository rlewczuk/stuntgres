package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func sequenceScalar(t *testing.T, s *session.Session, q string, want int64) {
	t.Helper()
	r := enginetest.SessionSQL(t, s, q)
	defer r.Rows.Close()
	if got := enginetest.Tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{want}}) {
		t.Fatalf("%s got %v want %d", q, got, want)
	}
}

func TestSequenceAllocationConcurrentSessions(t *testing.T) {
	for _, cache := range []string{"1", "7"} {
		t.Run("cache"+cache, func(t *testing.T) {
			e := engine.New()
			owner := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, owner, "CREATE SEQUENCE s CACHE "+cache)
			const clients = 8
			const each = 40
			sessions := make([]*session.Session, clients)
			for i := range sessions {
				sessions[i] = enginetest.StartSession(t, e, nil)
			}
			values := make(chan int64, clients*each)
			failures := make(chan error, clients)
			var wg sync.WaitGroup
			for _, s := range sessions {
				wg.Add(1)
				go func(s *session.Session) {
					defer wg.Done()
					for range each {
						op, err := s.Acquire(context.Background())
						if err != nil {
							failures <- err
							return
						}
						r, err := e.Execute(op, mustLockStatement("SELECT nextval('s')"), 0, nil)
						if err == nil {
							rows := enginetest.Tuples(r)
							r.Rows.Close()
							values <- rows[0][0].(int64)
						}
						op.Done()
						if err != nil {
							failures <- err
							return
						}
					}
				}(s)
			}
			wg.Wait()
			close(values)
			close(failures)
			for err := range failures {
				t.Fatal(err)
			}
			seen := make(map[int64]bool)
			for v := range values {
				if seen[v] {
					t.Fatalf("duplicate %d", v)
				}
				seen[v] = true
			}
			if len(seen) != clients*each {
				t.Fatal("missing allocations", len(seen))
			}
			if cache == "1" {
				for v := int64(1); v <= clients*each; v++ {
					if !seen[v] {
						t.Fatalf("CACHE1 bag missing %d", v)
					}
				}
			}
		})
	}
}

func TestSequenceLocksCachedCallsAndCancellation(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s CACHE 4")
	sequenceScalar(t, b, "SELECT nextval('s')", 1)
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "ALTER SEQUENCE s RESTART 20")
	ctx, cancel := context.WithCancel(context.Background())
	done := enginetest.LockAsync(e, b, ctx, "SELECT nextval('s')", false)
	enginetest.LockPending(t, e, 1)
	cancel()
	enginetest.LockAwait(t, done, context.Canceled)
	done = enginetest.LockAsync(e, b, context.Background(), "SELECT nextval('s')", false)
	enginetest.LockPending(t, e, 1)
	enginetest.TransactionEnd(t, a, tx, false)
	enginetest.LockAwait(t, done, nil)
	sequenceScalar(t, b, "SELECT currval('s')", 2)
	tx = enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "ALTER SEQUENCE s RESTART 30")
	done = enginetest.LockAsync(e, b, context.Background(), "SELECT currval('s')", false)
	enginetest.LockPending(t, e, 1)
	enginetest.TransactionEnd(t, a, tx, true)
	enginetest.LockAwait(t, done, nil)
	sequenceScalar(t, b, "SELECT nextval('s')", 30)
	enginetest.LockEmpty(t, e)
}

func TestSequenceACLAnyOfAndLateRevocation(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER seq_owner")
	enginetest.RoleExecute(t, e, op, "CREATE USER seq_reader")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_owner")
	withPublicSchemaUnitOwner(t, e, "seq_owner", func() {
		enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE s")
	})
	ref := enginetest.RoleTableRef(t, e, "s")
	owner := op.RoleState().CurrentUser
	reader, _ := e.Roles().Lookup("seq_reader")
	grant := func(p authorization.Privileges) {
		t.Helper()
		if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: p}}}); err != nil {
			t.Fatal(err)
		}
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_reader")
	for _, p := range []authorization.Privileges{authorization.UsagePrivilege, authorization.UpdatePrivilege} {
		grant(p)
		r := enginetest.RoleExecute(t, e, op, "SELECT nextval('s')")
		r.Rows.Close()
	}
	for _, tc := range []struct {
		mask    authorization.Privileges
		allowed map[string]bool
	}{
		{authorization.UsagePrivilege, map[string]bool{"SELECT nextval('s')": true, "SELECT currval('s')": true, "SELECT lastval()": true}},
		{authorization.SelectPrivilege, map[string]bool{"SELECT currval('s')": true, "SELECT lastval()": true, "SELECT * FROM s": true}},
		{authorization.UpdatePrivilege, map[string]bool{"SELECT nextval('s')": true, "SELECT setval('s',40)": true}},
		{0, nil},
	} {
		grant(tc.mask)
		for _, q := range []string{"SELECT nextval('s')", "SELECT currval('s')", "SELECT lastval()", "SELECT setval('s',40)", "SELECT * FROM s"} {
			if tc.allowed[q] {
				r := enginetest.RoleExecute(t, e, op, q)
				r.Rows.Close()
			} else {
				enginetest.RoleError(t, e, op, q, "42501", "permission denied for sequence s", "")
			}
		}
	}
	grant(authorization.SelectPrivilege)
	r := enginetest.RoleExecute(t, e, op, "SELECT currval('s')")
	r.Rows.Close()
	enginetest.RoleError(t, e, op, "SELECT nextval('s')", "42501", "permission denied for sequence s", "")
	grant(authorization.UpdatePrivilege)
	r = enginetest.RoleExecute(t, e, op, "SELECT setval('s',40)")
	r.Rows.Close()
	enginetest.RoleError(t, e, op, "SELECT currval('s')", "42501", "permission denied for sequence s", "")
	grant(authorization.UsagePrivilege)
	description, err := e.Describe(op, enginetest.Statement(t, "SELECT nextval('s')"))
	if err != nil {
		t.Fatal(err)
	}
	grant(0)
	if scope, scopeErr := e.StartStatement(op); scopeErr != nil {
		t.Fatal(scopeErr)
	} else {
		scope.Expected = &description
		_, err = scope.Execute(enginetest.Statement(t, "SELECT nextval('s')"), 0, nil)
		err = scope.Finish(err)
		if err == nil {
			t.Fatal("revoked prepared privilege accepted")
		}
	}
}

func TestSequenceRestartLifecycleMatrix(t *testing.T) {
	for _, end := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "failed commit", "physical end", "reset", "restore"} {
		t.Run(end, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
			if end == "failed commit" {
				enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT NOT NULL)")
			}
			sequenceScalar(t, b, "SELECT nextval('s')", 1)
			controlsRequire(t, a, "BEGIN")
			enginetest.SessionSQL(t, a, "ALTER SEQUENCE s RESTART 100")
			queued := enginetest.LockAsync(e, b, context.Background(), "SELECT nextval('s')", false)
			enginetest.LockPending(t, e, 1)
			want := int64(2)
			switch end {
			case "COMMIT AND CHAIN":
				controlsRequire(t, a, end)
				want = 100
			case "ROLLBACK AND CHAIN":
				controlsRequire(t, a, end)
			case "failed commit":
				controlsAbort(t, a)
				controlsRequire(t, a, "COMMIT")
			case "physical end":
				a.RequestEnd(session.CleanClose)
				if err := a.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "reset":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "restore":
				if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE SEQUENCE s START 9;")); err != nil {
					t.Fatal(err)
				}
			}
			if end == "reset" || end == "restore" {
				enginetest.LockAwait(t, queued, ids.ErrCatalogReplaced)
				controlsRequire(t, a, "ROLLBACK")
				if end == "reset" {
					enginetest.SessionSQL(t, b, "CREATE SEQUENCE s START 9")
				}
				sequenceScalar(t, b, "SELECT nextval('s')", 9)
			} else {
				enginetest.LockAwait(t, queued, nil)
				sequenceScalar(t, b, "SELECT currval('s')", want)
				if a.TransactionState() == session.TxActive {
					controlsRequire(t, a, "ROLLBACK")
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestSequenceRestartBlocksEveryOperation(t *testing.T) {
	for _, q := range []string{"SELECT nextval('s')", "SELECT currval('s')", "SELECT lastval()", "SELECT setval('s',40)"} {
		t.Run(q, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE SEQUENCE s CACHE 7")
			sequenceScalar(t, b, "SELECT nextval('s')", 1)
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "ALTER SEQUENCE s RESTART 100")
			row := enginetest.SessionSQL(t, b, "SELECT * FROM s")
			row.Rows.Close()
			queued := enginetest.LockAsync(e, b, context.Background(), q, false)
			enginetest.LockPending(t, e, 1)
			enginetest.TransactionEnd(t, a, tx, false)
			enginetest.LockAwait(t, queued, nil)
			enginetest.LockEmpty(t, e)
		})
	}
}

// currvalIs reports whether a session memory holds currval want.
func currvalIs(m *sequence.Memory, want int64) bool {
	v, ok := m.Current()
	return ok && v == want
}

// currvalValid reports whether a session memory holds a currval.
func currvalValid(m *sequence.Memory) bool {
	_, ok := m.Current()
	return ok
}

// currvalValue is the remembered currval value, valid or not.
func currvalValue(m *sequence.Memory) int64 {
	v, _ := m.Current()
	return v
}

// cachedSequencePathIs reports whether the session caches exactly key.
func cachedSequencePathIs(s *session.Session, key sequence.PathKey) bool {
	cached, ok := core.PayloadOf(s).Sequences.CachedPathKey()
	return ok && cached == key
}

func controlsRequire(t *testing.T, s *session.Session, q string) {
	t.Helper()
	if err := controlsExecute(t, s, q); err != nil {
		t.Fatal(q, err)
	}
}

func controlsAbort(t *testing.T, s *session.Session) {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	_, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, "INSERT INTO t VALUES(NULL)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23502" {
		t.Fatal(err)
	}
}

func controlsExecute(t *testing.T, s *session.Session, q string) error {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, q), 0, nil)
	if r.Rows != nil {
		r.Rows.Close()
	}
	if err == nil && r.Count != 0 {
		t.Fatal(q, r.Count)
	}
	return err
}

func TestSepgsqlSimulationSequenceCreate(t *testing.T) {
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			e, events := sepgsqlSequenceEngine(t, nil)
			s := sepgsqlSequenceActor(t, e)
			for _, name := range []string{"created", "selected"} {
				q := "CREATE SEQUENCE " + name + " START 5 RESTART 9 CACHE 3"
				before := e.CatalogRead()
				ast, description := sequencePreparedDescription(t, e, s, q)
				if description.Parameters.Count != 0 || len(description.Results) != 0 || e.CatalogRead().State() != before.State() {
					t.Fatal("Prepare created a sequence or changed utility signature")
				}
				var expected *core.Description
				if prepared {
					expected = &description
				}
				for range 2 {
					r, err := sequencePreparedExecute(e, s, ast, expected)
					if err != nil || r.Count != 0 || r.Rows != nil {
						t.Fatal("CREATE result", r, err)
					}
					read := e.CatalogRead()
					ref := enginetest.CatalogRef(t, e, read, name)
					d := read.MustRelation(ref.Address)
					want := "P"
					if name == "selected" {
						want = "Q"
					}
					sepgsqlSequenceLabel(t, read, ref.Address, want)
					if d.Namespace() != public || d.Owner != s.Authorization.State.CurrentUser ||
						d.Sequence.Start != 5 || d.Sequence.Cache != 3 || e.Storage.Sequences[ref.Address] == nil {
						t.Fatal("real sequence options/owner/namespace/generator", d)
					}
					sequenceCreateRestartRow(t, e, s, name, 9, false)
					// The same retained CREATE handle must create afresh after DROP.
					enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('A')")
					enginetest.SessionSQL(t, s, "DROP SEQUENCE "+name)
					enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('R')")
				}
			}
			creates := sepgsqlSequenceEvents(events())
			if len(creates) != 4 {
				t.Fatal("one actual postcreate per execution", creates)
			}
			for _, event := range creates {
				if event.Subject != "R" || !reflect.DeepEqual(event.Required, []string{"create"}) || len(event.Denied) != 0 {
					t.Fatal("actual subject/mask", event)
				}
			}
		})
	}
	t.Run("diagnostics_order_and_no_effects", func(t *testing.T) {
		for _, denied := range []string{"add_name", "create", "transition", "permissive"} {
			t.Run(denied, func(t *testing.T) {
				e, events := sepgsqlSequenceEngine(t, func(c *sepgsql.SimulationConfig) {
					class, target, permission := "db_sequence", "P", "create"
					if denied == "add_name" {
						class, permission = "db_schema", "add_name"
					}
					if denied == "transition" {
						target = "Q"
					}
					c.Policy.Rules = append(c.Policy.Rules, sepgsql.Rule{Source: "R", Target: target, Class: class, Deny: []string{permission}})
					c.Permissive = denied == "permissive"
				})
				s := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, s, "CREATE TABLE sentinel(v integer)")
				enginetest.SessionSQL(t, s, "INSERT INTO sentinel VALUES(7)")
				enginetest.SessionSQL(t, s, "CREATE SEQUENCE untouched START 17 CACHE 3")
				sequenceScalar(t, s, "SELECT nextval('untouched')", 17)
				enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('R')")
				untouched := enginetest.CatalogRef(t, e, e.CatalogRead(), "untouched")
				sentinel := enginetest.CatalogRef(t, e, e.CatalogRead(), "sentinel")
				keptGenerator, keptHeap := *e.Storage.Sequences[untouched.Address], e.Storage.Relations[sentinel.Address]
				actor, revision := s.Authorization.State, s.Authorization.Revision
				eventStart := len(events())
				name := "denied"
				if denied == "transition" {
					name = "selected"
				}
				before, roles, generators := e.CatalogRead(), e.RoleStore.Published, len(e.Storage.Sequences)
				_, err := sequenceCreateRestartCommand(t, e, s, "CREATE SEQUENCE "+name+" OWNED BY absent.c", true)
				if denied == "permissive" {
					sequenceOwnerDiagnostic(t, err, "42P01", `relation "absent" does not exist`, "")
				} else {
					sequenceOwnerDiagnostic(t, err, "42501", "SELinux: security policy violation", "")
				}
				if e.CatalogRead().State() != before.State() || e.RoleStore.Published != roles || len(e.Storage.Sequences) != generators ||
					s.Transactions.Catalog != nil || s.Transactions.State != session.TxIdle {
					t.Fatal("failed create/OWNED BY published prospective state")
				}
				if *e.Storage.Sequences[untouched.Address] != keptGenerator || e.Storage.Relations[sentinel.Address] != keptHeap ||
					!reflect.DeepEqual(s.Authorization.State, actor) || s.Authorization.Revision != revision {
					t.Fatal("denied creation changed existing data/generator/session authority")
				}
				sepgsqlSequenceAbsent(t, e, public, name)
				creates := sepgsqlSequenceEvents(events()[eventStart:])
				if denied == "add_name" {
					if len(creates) != 0 {
						t.Fatal("CREATE checked after ADD_NAME denial", creates)
					}
				} else if len(creates) != 1 || !reflect.DeepEqual(creates[0].Denied, []string{"create"}) ||
					creates[0].Permissive != (denied == "permissive") || creates[0].Target != map[bool]string{false: "P", true: "Q"}[denied == "transition"] {
					t.Fatal("true denied create audit", creates)
				}
				if denied == "permissive" {
					enginetest.SessionSQL(t, s, "CREATE SEQUENCE allowed_permissive")
					sepgsqlSequenceLabel(t, e.CatalogRead(), enginetest.CatalogRef(t, e, e.CatalogRead(), "allowed_permissive").Address, "P")
				}
			})
		}
		e, events := sepgsqlSequenceEngine(t, nil)
		s := sepgsqlSequenceActor(t, e)
		enginetest.SessionSQL(t, s, "CREATE SEQUENCE kept START 5 CACHE 3")
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "kept")
		before, generator, n := e.CatalogRead(), *e.Storage.Sequences[ref.Address], len(sepgsqlSequenceEvents(events()))
		oid, revision := e.Catalog.NextOID(), e.Catalog.NextRevision()
		notices, err := sequenceCreateRestartCommand(t, e, s, "CREATE SEQUENCE IF NOT EXISTS kept INCREMENT 0", true)
		want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "kept" already exists, skipping`}}
		if err != nil || !reflect.DeepEqual(notices, want) || e.CatalogRead().State() != before.State() ||
			*e.Storage.Sequences[ref.Address] != generator || len(sepgsqlSequenceEvents(events())) != n {
			t.Fatal("genuine skip emitted create/changed state", notices, err)
		}
		if e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision {
			t.Fatal("initial existing skip reserved sequence identity/revision")
		}
		for _, tc := range []struct{ q, code, message string }{
			{"CREATE SEQUENCE missing INCREMENT 0", "22023", "INCREMENT must not be zero"},
			{"CREATE SEQUENCE IF NOT EXISTS missing INCREMENT 0", "22023", "INCREMENT must not be zero"},
			{"CREATE SEQUENCE missing OWNED BY absent.c", "42P01", `relation "absent" does not exist`},
		} {
			oid, revision := e.Catalog.NextOID(), e.Catalog.NextRevision()
			_, err := sequenceCreateRestartCommand(t, e, s, tc.q, false)
			sequenceOwnerDiagnostic(t, err, tc.code, tc.message, "")
			if e.CatalogRead().State() != before.State() || *e.Storage.Sequences[ref.Address] != generator {
				t.Fatal("option/ownership error changed existing state")
			}
			sepgsqlSequenceAbsent(t, e, public, "missing")
			if tc.code == "22023" && (e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision) {
				t.Fatal("early invalid options reserved sequence identity/revision")
			}
		}
	})
	t.Run("transaction_savepoint_and_consumers", func(t *testing.T) {
		e, events := sepgsqlSequenceEngine(t, nil)
		s, peer := sepgsqlSequenceActor(t, e), sepgsqlSequenceActor(t, e)
		for _, end := range []string{"ROLLBACK", "ROLLBACK TO mark", "RELEASE mark"} {
			before := e.CatalogRead()
			enginetest.SessionSQL(t, s, "BEGIN")
			enginetest.SessionSQL(t, s, "SAVEPOINT mark")
			enginetest.SessionSQL(t, s, "CREATE SEQUENCE private_sequence")
			e.Mu.Lock()
			private := e.PrivateCatalogLocked(s.Transactions.Catalog)
			e.Mu.Unlock()
			address := private.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: public, Name: "private_sequence"})
			sepgsqlSequenceLabel(t, private, address, "P")
			if e.CatalogRead().State() != before.State() || s.Transactions.Catalog.Generators[address] == nil {
				t.Fatal("transaction sequence label/generator escaped private owner")
			}
			_, err := sequencePreparedExecute(e, peer, enginetest.Statement(t, "SELECT * FROM private_sequence"), nil)
			sequenceCreateRestartDiagnostic(t, err, "42P01", `relation "private_sequence" does not exist`, "15")
			enginetest.SessionSQL(t, s, end)
			if end != "ROLLBACK" {
				enginetest.SessionSQL(t, s, "COMMIT")
			}
			if end == "RELEASE mark" {
				sepgsqlSequenceLabel(t, e.CatalogRead(), address, "P")
				enginetest.SessionSQL(t, s, "DROP SEQUENCE private_sequence")
			} else if e.CatalogRead().State() != before.State() || e.Storage.Sequences[address] != nil {
				t.Fatal("rollback retained sequence, label or generator")
			}
		}
		for _, q := range []string{
			"CREATE TABLE serial_owner(id serial)",
			"CREATE TABLE identity_owner(id integer GENERATED ALWAYS AS IDENTITY)",
			"CREATE TABLE add_owner(id integer NOT NULL)",
			"ALTER TABLE add_owner ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY",
		} {
			enginetest.SessionSQL(t, s, q)
		}
		creates := sepgsqlSequenceEvents(events())
		count := 0
		for address, d := range e.CatalogRead().Descriptors() {
			if r, ok := d.(*catalog.Relation); ok && r.RelationKind == catalog.SequenceRelation {
				sepgsqlSequenceLabel(t, e.CatalogRead(), address, "P")
				if r.Sequence.Owned == (ids.ObjectAddress{}) || e.Storage.Sequences[address] == nil {
					t.Fatal("serial/identity ownership or generator lost", r)
				}
				count++
			}
		}
		if count != 3 || len(creates) != 6 {
			t.Fatal("same stage consumers double-hooked or omitted", count, creates)
		}
	})
	t.Run("consumer_denial_atomicity", func(t *testing.T) {
		for _, q := range []string{
			"CREATE TABLE failed(id serial)",
			"CREATE TABLE failed(id integer GENERATED ALWAYS AS IDENTITY)",
			"ALTER TABLE kept ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY",
		} {
			t.Run(q, func(t *testing.T) {
				e, events := sepgsqlSequenceEngine(t, func(c *sepgsql.SimulationConfig) {
					c.Policy.Rules = append(c.Policy.Rules, sepgsql.Rule{Source: "R", Target: "P", Class: "db_sequence", Deny: []string{"create"}})
				})
				s := sepgsqlSequenceActor(t, e)
				enginetest.SessionSQL(t, s, "CREATE TABLE kept(id integer NOT NULL)")
				before, generators := e.CatalogRead(), len(e.Storage.Sequences)
				_, err := sequencePreparedExecute(e, s, enginetest.Statement(t, q), nil)
				sequenceOwnerDiagnostic(t, err, "42501", "SELinux: security policy violation", "")
				if e.CatalogRead().State() != before.State() || len(e.Storage.Sequences) != generators {
					t.Fatal("denied owned sequence retained table/default/link/label")
				}
				if creates := sepgsqlSequenceEvents(events()); len(creates) != 1 || !reflect.DeepEqual(creates[0].Denied, []string{"create"}) {
					t.Fatal("sequence CREATE missing/doubled", creates)
				}
			})
		}
	})
	t.Run("ordinary_sql_create_precedes_mac", func(t *testing.T) {
		e, events := sepgsqlSequenceEngine(t, nil)
		s := sepgsqlSequenceActor(t, e)
		enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('A')")
		enginetest.SessionSQL(t, s, "CREATE USER sequence_reader")
		enginetest.SessionSQL(t, s, "CREATE SCHEMA private")
		enginetest.SessionSQL(t, s, "CREATE SEQUENCE private.kept")
		enginetest.SessionSQL(t, s, "SET SESSION AUTHORIZATION sequence_reader")
		enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('R')")
		before, n := e.CatalogRead(), len(sepgsqlSequenceEvents(events()))
		for _, tc := range []struct{ q, code, message string }{
			{"CREATE SEQUENCE private.missing", "42501", "permission denied for schema private"},
			{"CREATE SEQUENCE private.missing INCREMENT 0", "22023", "INCREMENT must not be zero"},
			{"CREATE SEQUENCE IF NOT EXISTS private.kept INCREMENT 0", "42501", "permission denied for schema private"},
		} {
			_, err := sequenceCreateRestartCommand(t, e, s, tc.q, true)
			sequenceOwnerDiagnostic(t, err, tc.code, tc.message, "")
		}
		if e.CatalogRead().State() != before.State() || len(sepgsqlSequenceEvents(events())) != n {
			t.Fatal("ordinary SQL refusal reached sequence hook")
		}
	})
	t.Run("lazy_temporary_actual_label_and_cleanup", func(t *testing.T) {
		for _, deny := range []bool{false, true} {
			t.Run(map[bool]string{false: "allow", true: "deny"}[deny], func(t *testing.T) {
				e, events := sepgsqlSequenceEngine(t, func(c *sepgsql.SimulationConfig) {
					c.Policy.CreateTransitions = append(c.Policy.CreateTransitions,
						sepgsql.CreateTransition{Source: "R", Parent: "P", Class: "db_schema", Name: "pg_temp", Result: "N"},
						sepgsql.CreateTransition{Source: "R", Parent: "P", Class: "db_schema", Name: "pg_toast_temp", Result: "N"})
					c.Policy.Rules = append(c.Policy.Rules,
						sepgsql.Rule{Source: "R", Target: "N", Class: "db_schema", Allow: []string{"create", "search", "add_name"}},
						sepgsql.Rule{Source: "R", Target: "N", Class: "db_sequence", Allow: []string{"create", "get_value"}})
					if deny {
						c.Policy.Rules = append(c.Policy.Rules, sepgsql.Rule{Source: "R", Target: "N", Class: "db_sequence", Deny: []string{"create"}})
					}
				})
				s := sepgsqlSequenceActor(t, e)
				before, generators := e.CatalogRead(), len(e.Storage.Sequences)
				_, err := sequenceCreateRestartCommand(t, e, s, "CREATE TEMP SEQUENCE first_temp START 5 RESTART 9", false)
				if deny {
					sequenceOwnerDiagnostic(t, err, "42501", "SELinux: security policy violation", "")
					if e.CatalogRead().State() != before.State() || len(e.Storage.Sequences) != generators ||
						s.TemporaryNamespace != (ids.ObjectAddress{}) || s.Transactions.Catalog != nil {
						t.Fatal("failed first TEMP CREATE installed namespace intent/labels/generator")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				namespace := s.TemporaryNamespace
				enginetest.AssertTemporaryNamespacePair(t, e, s.SessionToken, namespace)
				firstRead := e.CatalogRead()
				first := firstRead.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "first_temp"})
				sepgsqlSequenceLabel(t, firstRead, first, "N")
				enginetest.SessionSQL(t, s, "CREATE TEMP SEQUENCE second_temp")
				secondRead := e.CatalogRead()
				second := secondRead.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "second_temp"})
				sepgsqlSequenceLabel(t, secondRead, second, "N")
				key := catalog.SecurityLabelKey{Object: namespace, Provider: "selinux"}
				if secondRead.Get(namespace) != firstRead.Get(namespace) || secondRead.State().SecurityLabel(key) != firstRead.State().SecurityLabel(key) {
					t.Fatal("existing TEMP namespace relabeled/recreated")
				}
				peer := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, peer, "CREATE TEMP SEQUENCE peer_temp")
				peerRead := e.CatalogRead()
				peerSequence := peerRead.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peer.TemporaryNamespace, Name: "peer_temp"})
				peerGenerator := e.Storage.Sequences[peerSequence]
				s.RequestEnd(session.CleanClose)
				if err := s.WaitEnd(t.Context()); err != nil {
					t.Fatal(err)
				}
				for _, address := range []ids.ObjectAddress{first, second} {
					if e.CatalogRead().Get(address) != nil || e.Storage.Sequences[address] != nil ||
						e.CatalogRead().State().SecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"}) != nil {
						t.Fatal("physical TEMP cleanup leaked sequence state")
					}
				}
				if e.CatalogRead().Get(peerSequence) != peerRead.Get(peerSequence) || e.Storage.Sequences[peerSequence] != peerGenerator {
					t.Fatal("TEMP cleanup changed a peer")
				}
				if creates := sepgsqlSequenceEvents(events()); len(creates) != 3 || creates[0].Target != "N" || creates[1].Target != "N" {
					t.Fatal("TEMP transition did not use genuine private namespace label", creates)
				}
			})
		}
	})
	t.Run("quoted_actual_schema_then_basename_transition", func(t *testing.T) {
		e, events := sepgsqlSequenceEngine(t, func(c *sepgsql.SimulationConfig) {
			c.Policy.CreateTransitions = append(c.Policy.CreateTransitions, sepgsql.CreateTransition{Source: "R", Parent: "P", Class: "db_sequence", Name: "odd.name", Result: "Q"})
		})
		admin := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, admin, `CREATE SCHEMA "odd.schema"`)
		s := sepgsqlSequenceActor(t, e)
		n := len(events())
		enginetest.SessionSQL(t, s, `CREATE SEQUENCE "odd.schema"."odd.name"`)
		namespace := schemaRef(t, e, "odd.schema").Address
		address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "odd.name"})
		sepgsqlSequenceLabel(t, e.CatalogRead(), address, "Q")
		// This native engine's actual database name is empty and must be quoted.
		ordered := 0
		for _, event := range events()[n:] {
			if event.Class == "db_schema" && reflect.DeepEqual(event.Required, []string{"add_name"}) {
				if ordered != 0 || event.Name != `""."odd.schema"` || event.Target != "P" {
					t.Fatal("actual quoted namespace ADD_NAME order/name/label", event)
				}
				ordered++
			}
			if event.Class == "db_sequence" {
				if ordered != 1 || event.Name != `""."odd.schema"."odd.name"` ||
					event.Target != "Q" || !reflect.DeepEqual(event.Required, []string{"create"}) {
					t.Fatal("actual basename transition/quoted sequence audit", event)
				}
				ordered++
			}
			if event.Class == "db_column" {
				t.Fatal("explicit sequence created column hook", event)
			}
		}
		if ordered != 2 {
			t.Fatal("missing schema/sequence postcreate decisions", ordered)
		}
	})
}
