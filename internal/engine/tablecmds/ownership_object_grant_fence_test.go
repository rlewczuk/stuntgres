package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// ObjectGrant remains an immediate lock-safe role utility. This exercises its
// real retained-message candidate and outer publication, not a mock ACL stage.
func TestRoleOwnershipObjectGrantMessageDatabaseFence(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, outcome := range []string{"database", "cancel", "healthy"} {
			name := map[bool]string{false: "committed", true: "private"}[private] + "/" + outcome
			t.Run(name, func(t *testing.T) {
				// Reuse genuine typed rows, constraints and index roots as immutable
				// witnesses. Only ObjectGrant changes in this tested message.
				e, session, root, committed, changes := ownershipPartitionConstraintFixture(t, private)
				bootstrap, done := roleOperation(t, e, "postgres")
				reader := roleAttributeCreate(t, e, bootstrap, "object_fence_reader", false, parser.RoleOptions{}).Ref
				done()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				message := batchUnitStart(t, session, ctx)
				defer message.end(context.Canceled)
				if private {
					session.Transactions.Catalog = changes
				}
				auth, err := core.OperationAuthorization(message.op)
				if err != nil {
					t.Fatal(err)
				}
				actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
				if err != nil || actor.Superuser {
					t.Fatal("fixture is not an ordinary actor", err)
				}
				dbRole := auth.Roles.LookupRef("pg_database_owner")
				table := auth.Catalog.MustRelation(root.Address)
				if table.Owner != dbRole || table.Owner == actor.Ref || auth.DatabasePredecessor != committed || auth.PairedCatalog != auth.Catalog {
					t.Fatal("fixture lacks canonical inherited DB-owner authority")
				}
				if private && auth.Catalog.Get(catalog.CurrentDatabaseAddress) == committed {
					t.Fatal("private DB authority is not distinct from committed predecessor")
				}
				if err := auth.Check(access.Requirement{Object: root, OwnerOnly: true}); err != nil {
					t.Fatal("selected DB owner cannot authorize actual table", err)
				}
				checkReader := func(view core.AuthorizationView, allowed bool) {
					t.Helper()
					view.State.CurrentUser = reader
					err := view.Check(access.Requirement{Object: root, Privileges: authorization.SelectPrivilege})
					if allowed {
						if err != nil {
							t.Fatal("actual staged reader right is absent", err)
						}
					} else {
						var diagnostic *sqlstate.Error
						if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
							t.Fatal("reader acquired an unpublished SELECT right", err)
						}
					}
				}
				checkReader(auth, false)
				oldRoles, oldCatalog := e.RoleStore.Published, e.Catalog.State
				sessionRevision, epoch := session.Authorization.Revision, oldCatalog.Epoch()
				oldACL := authorization.ACLValue{Explicit: table.Security.ACL.Explicit, Entries: append([]authorization.ACLEntry(nil), table.Security.ACL.Entries...)}
				oldKeys := append([]catalog.UniqueConstraint(nil), table.Constraints.Unique...)
				before := indexMaintenanceCapture(e)
				leaf := roleTableRef(t, e, "leaf").Address
				rows := append([]storage.RowVersion(nil), e.Storage.Relations[leaf].Versions...)
				for i := range rows {
					rows[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
				}
				if len(rows) != 1 || len(oldKeys) != 1 {
					t.Fatal("fixture lacks real data and constraint witnesses")
				}
				request := &parser.ObjectGrant{Privileges: []parser.GrantPrivilege{{Name: "select"}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "root"}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "object_fence_reader"}}}, GrantOption: true}
				result, err := message.step(request)
				if err != nil || result.Rows != nil || result.Count != 0 {
					t.Fatal("actual retained ObjectGrant failed", err)
				}
				retained, err := core.OperationAuthorization(message.op)
				if err != nil {
					t.Fatal(err)
				}
				pending := retained.Catalog.MustRelation(root.Address)
				if pending == table || retained.RoleRecord == oldRoles || retained.PairedCatalog != retained.Catalog || session.Transactions.Catalog.Descriptors[root.Address] != pending {
					t.Fatal("utility did not stage real paired ACL/role/catalog roots")
				}
				found := false
				for _, entry := range pending.Security.ACL.Entries {
					if !entry.Grantee.Public && entry.Grantee.Role == reader {
						if entry.Grantor != dbRole || entry.Privileges != authorization.SelectPrivilege || entry.GrantOptions != authorization.SelectPrivilege {
							t.Fatal("actual grant lacks canonical owner origin and option", entry)
						}
						found = true
					}
				}
				if !found || e.Catalog.State != oldCatalog || e.RoleStore.Published != oldRoles || !reflect.DeepEqual(table.Security.ACL, oldACL) {
					t.Fatal("staging lost the grant or published/mutated a prefix")
				}
				checkReader(retained, true)
				pendingRoles, pendingDB := retained.RoleRecord, retained.Catalog.Get(catalog.CurrentDatabaseAddress)
				if outcome == "database" {
					stage := e.BeginCatalogStage(e.CatalogRead())
					database := *e.Catalog.State.MustDatabase(catalog.CurrentDatabaseAddress)
					if private {
						database.Owner = actor.Ref
					} else {
						database.Owner = oldRoles.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
					}
					database.ObjectRevision++
					if err := stage.Replace(&database); err != nil {
						t.Fatal(err)
					}
					ownershipUnitPublish(t, e, stage)
					e.DiscardCatalogStage(stage)
					for address, descriptor := range oldCatalog.Descriptors() {
						if address != catalog.CurrentDatabaseAddress && e.Catalog.State.Get(address) != descriptor {
							t.Fatal("DB-only replacement changed object/associated identities")
						}
					}
					before = indexMaintenanceCapture(e)
				} else if outcome == "cancel" {
					cancel()
				}
				if e.RoleStore.Published != oldRoles || e.Catalog.State.Epoch() != epoch || session.Authorization.Revision != sessionRevision {
					t.Fatal("replacement changed role/session/epoch authority")
				}
				err = message.end(nil)
				if outcome == "healthy" {
					if err != nil {
						t.Fatal("unchanged predecessor rejected legitimate retained grant", err)
					}
					current := catalogAuthorization(e, e.CatalogRead())
					checkReader(current, true)
					if e.Catalog.State.Get(root.Address) != pending || e.RoleStore.Published != pendingRoles || !reflect.DeepEqual(e.Storage.Relations[leaf].Versions, rows) || !reflect.DeepEqual(pending.Constraints.Unique, oldKeys) {
						t.Fatal("healthy grant lost candidate or changed data/constraints")
					}
					for address, index := range before.roots {
						if e.Storage.IndexRoots[address] != index {
							t.Fatal("healthy ACL-only grant changed index root")
						}
					}
					return
				}
				if outcome == "cancel" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("missing final cancellation veto", err)
					}
				} else {
					var conflict catalog.Conflict
					if !errors.Is(err, ids.ErrCatalogReplaced) && !errors.As(err, &conflict) {
						t.Fatal("missing retained ObjectGrant DB predecessor veto", err)
					}
				}
				indexMaintenanceUnchanged(t, e, before)
				checkReader(catalogAuthorization(e, e.CatalogRead()), false)
				checkReader(retained, true)
				if e.RoleStore.Published != oldRoles || !reflect.DeepEqual(e.Storage.Relations[leaf].Versions, rows) || !reflect.DeepEqual(table.Security.ACL, oldACL) || !reflect.DeepEqual(table.Constraints.Unique, oldKeys) || retained.RoleRecord != pendingRoles || retained.Catalog.Get(catalog.CurrentDatabaseAddress) != pendingDB || auth.DatabasePredecessor != committed {
					t.Fatal("veto mutated old ACL/keys/storage or retained authority roots")
				}
			})
		}
	}
}

func roleAttributeCreate(t *testing.T, e *core.Engine, op *session.Operation, name string, login bool, o parser.RoleOptions) authorization.RoleDescriptor {
	t.Helper()
	roleAttributeApply(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: name}, DefaultLogin: login, Options: o})
	d, ok := e.Roles().Lookup(name)
	if !ok {
		t.Fatal("missing role", name)
	}
	return d
}

func batchUnitStart(t *testing.T, s *session.Session, ctx context.Context) *batchUnitMessage {
	t.Helper()
	op, _, err := s.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := core.EngineOf(s).StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err = scope.RetainMessage(); err != nil {
		_ = scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	return &batchUnitMessage{scope: scope, op: op}
}

func indexMaintenanceCapture(e *core.Engine) indexMaintenanceSnapshot {
	s := indexMaintenanceSnapshot{catalog: e.Catalog.State, roots: make(map[ids.ObjectAddress]*storage.IndexRoot, len(e.Storage.IndexRoots)), stores: make(map[ids.ObjectAddress]*storage.Relation, len(e.Storage.Relations))}
	for a, r := range e.Storage.IndexRoots {
		s.roots[a] = r
	}
	for a, r := range e.Storage.Relations {
		s.stores[a] = r
	}
	return s
}

func ownershipUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publishCatalog(e, p, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func indexMaintenanceUnchanged(t *testing.T, e *core.Engine, before indexMaintenanceSnapshot) {
	t.Helper()
	if e.Catalog.State != before.catalog || len(e.Storage.IndexRoots) != len(before.roots) || len(e.Storage.Relations) != len(before.stores) {
		t.Fatal("failed operation published catalog/storage/root state")
	}
	for a, r := range before.roots {
		if e.Storage.IndexRoots[a] != r {
			t.Fatal("failed operation changed root", a)
		}
	}
	for a, r := range before.stores {
		if e.Storage.Relations[a] != r {
			t.Fatal("failed operation changed table storage", a)
		}
	}
}

func roleAttributeApply(t *testing.T, e *core.Engine, op *session.Operation, s parser.Statement) {
	t.Helper()
	r, err := e.Execute(op, s, 0, nil)
	if err != nil || r.Count != 0 || r.Rows != nil {
		t.Fatalf("attribute utility: result=%#v error=%v", r, err)
	}
}

type batchUnitMessage struct {
	scope  *core.StatementScope
	op     *session.Operation
	ended  bool
	result error
}

func (m *batchUnitMessage) step(stmt parser.Statement) (core.Result, error) {
	if m.scope.Finished {
		next, err := m.scope.NextStatement()
		if err != nil {
			return core.Result{}, err
		}
		m.scope = next
	}
	result, err := m.scope.Execute(stmt, 0, nil)
	finish := m.scope.Finish(err)
	if err == nil {
		err = finish
	}
	return result, err
}

func (m *batchUnitMessage) end(err error) error {
	if !m.ended {
		m.result = m.scope.FinishMessage(err)
		m.ended = true
		m.op.Done()
	}
	return m.result
}

type indexMaintenanceSnapshot struct {
	catalog *catalog.State
	roots   map[ids.ObjectAddress]*storage.IndexRoot
	stores  map[ids.ObjectAddress]*storage.Relation
}
