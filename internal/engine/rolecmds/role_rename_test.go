package rolecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func renameStatement(old, next string) *parser.RenameRole {
	return &parser.RenameRole{Role: parser.RoleSpec{Name: old}, Name: parser.Identifier{Text: next}}
}

func TestRoleRenamePublication(t *testing.T) {
	for _, password := range []string{"", "md500000000000000000000000000000000", "SCRAM-SHA-256$4096:c2FsdA==$c3RvcmVk:c2VydmVy"} {
		t.Run(password, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			d := roleAttributeCreate(t, e, op, "rename_original", true, parser.RoleOptions{})
			member := roleAttributeCreate(t, e, op, "rename_member", false, parser.RoleOptions{})
			if _, err := e.Execute(op, &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: d.Name}}, Members: []parser.RoleSpec{{Name: member.Name}}, Options: []parser.MembershipOption{{Name: "admin", Value: true}}}, 0, nil); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			base := e.RoleStore.Published
			d.Password = password
			d.Comment = "immutable comment"
			d.HasValidUntil = true
			d.ValidUntil = 12345
			root, err := base.Root.PutDescriptor(d)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			settings := base.Settings.WithRow(access.RoleSettingKey{Role: d.Ref}, []access.RoleSetting{{Name: "application_name", Value: "captured"}})
			seed := base.WithRoot(root).WithSettings(settings)
			if err := core.InstallOperationRolesLocked(e, op, seed); err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			base = e.RoleStore.Published
			retained := base.Root.Read(authorization.PrincipalRef{})
			version := e.RoleStore.NextRevision()
			e.Mu.Unlock()
			op.DetachNotices()
			if _, err := rolecmds.ExecuteRoleRename(e, &core.StatementScope{Op: op}, renameStatement(d.Name, "rename_result"), 0); err != nil {
				t.Fatal(err)
			}
			published := e.RoleStore.Published
			got, exists := published.Root.Read(authorization.PrincipalRef{}).Find(d.Ref)
			want := d
			want.Name = "rename_result"
			want.AuthIDVersion = version
			cleared := password != "" && password[:3] == "md5"
			if cleared {
				want.Password = ""
			}
			if !exists || got != want || published == base || published.SameAuthority(base) || published.Settings != settings {
				t.Fatal("identity, complete record or graph lineage lost", got, want)
			}
			if old, ok := retained.Find(d.Ref); !ok || old != d || retained.LookupRef("rename_original") != d.Ref || retained.LookupRef("rename_result") != (authorization.PrincipalRef{}) {
				t.Fatal("retained descriptor/name backing mutated")
			}
			if published.Root.Read(authorization.PrincipalRef{}).LookupRef("rename_original") != (authorization.PrincipalRef{}) || published.Root.Read(authorization.PrincipalRef{}).LookupRef("rename_result") != d.Ref {
				t.Fatal("name index did not move with principal")
			}
			beforeEdges, afterEdges := retained.Memberships(), published.Root.Read(authorization.PrincipalRef{}).Memberships()
			targetEdges := 0
			for _, edge := range beforeEdges {
				if edge.Role == d.Ref && edge.Member == member.Ref && edge.Admin && edge.OID != 0 {
					targetEdges++
				}
			}
			if targetEdges != 1 || !reflect.DeepEqual(beforeEdges, afterEdges) {
				t.Fatal("rename replaced or mutated membership identity")
			}
			if !reflect.DeepEqual(settings.Lookup(access.RoleSettingKey{Role: d.Ref}), []access.RoleSetting{{Name: "application_name", Value: "captured"}}) {
				t.Fatal("role-keyed settings changed")
			}
			notices := op.DetachNotices()
			if !cleared && len(notices) != 0 {
				t.Fatal(notices)
			}
			if cleared {
				want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "MD5 password cleared because of role rename"}
				if len(notices) != 1 || notices[0] != want {
					t.Fatalf("notices=%#v", notices)
				}
			}
		})
	}
}

func TestRoleRenamePrivateRollback(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	d := roleAttributeCreate(t, e, admin, "rename_private", false, parser.RoleOptions{})
	done()
	s := enginetest.StartSession(t, e, nil)
	ref := enginetest.TransactionBegin(t, s)
	op := enginetest.TransactionOperation(t, s)
	base := e.RoleStore.Published
	if _, err := rolecmds.ExecuteRoleRename(e, &core.StatementScope{Op: op}, renameStatement(d.Name, "rename_private_next"), 0); err != nil {
		t.Fatal(err)
	}
	journal := s.Transactions.Catalog
	if journal == nil || journal.RolesBase != base || journal.Roles == base || e.RoleStore.Published != base {
		t.Fatal("rename escaped owning journal")
	}
	private := journal.Roles
	if got, _ := private.Root.Read(authorization.PrincipalRef{}).Find(d.Ref); got.Name != "rename_private_next" {
		t.Fatal(got)
	}
	mark := core.CaptureSavepoint(s.Transactions, "rename_mark")
	if _, err := rolecmds.ExecuteRoleRename(e, &core.StatementScope{Op: op}, renameStatement("rename_private_next", "rename_private_later"), 0); err != nil {
		t.Fatal(err)
	}
	later := s.Transactions.Catalog.Roles
	if later == private || mark.Catalog.Roles != private {
		t.Fatal("savepoint retained a mutable candidate")
	}
	core.RestoreSavepoint(s.Transactions, mark, mark.Settings.Clone())
	e.Locks.Restore(s.Transactions.Ticket, mark.Locks)
	if s.Transactions.Catalog.Roles != private || s.Transactions.Catalog.RolesBase != base {
		t.Fatal("savepoint did not restore complete predecessor")
	}
	if got, _ := later.Root.Read(authorization.PrincipalRef{}).Find(d.Ref); got.Name != "rename_private_later" {
		t.Fatal("savepoint mutated later retained root")
	}
	op.Done()
	if enginetest.TransactionEnd(t, s, ref, false).Committed {
		t.Fatal("rollback committed")
	}
	if e.RoleStore.Published != base || s.Transactions.Catalog != nil {
		t.Fatal("rollback changed committed root or retained journal")
	}
	if got, _ := private.Root.Read(authorization.PrincipalRef{}).Find(d.Ref); got.Name != "rename_private_next" {
		t.Fatal("rollback mutated retained private root")
	}
}

func TestRoleRenameCanceledPublication(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	d := roleAttributeCreate(t, e, op, "rename_cancel", false, parser.RoleOptions{})
	base := e.RoleStore.Published
	frontier, revision := e.RoleStore.NextPrincipal(), e.RoleStore.NextRevision()
	ctx, cancel := context.WithCancel(op.Context())
	cancel()
	op.Ctx = ctx
	_, err := rolecmds.ExecuteRoleRename(e, &core.StatementScope{Op: op}, renameStatement(d.Name, "rename_cancel_next"), 0)
	if !errors.Is(err, context.Canceled) || e.RoleStore.Published != base || e.RoleStore.NextPrincipal() != frontier || e.RoleStore.NextRevision() != revision || len(op.DetachNotices()) != 0 {
		t.Fatal("canceled rename changed publication/allocator/notices", err)
	}
}

func TestRoleRenameCanceledWait(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	d := roleAttributeCreate(t, e, admin, "rename_wait", false, parser.RoleOptions{})
	done()
	holder, waiter := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	ref := enginetest.TransactionBegin(t, holder)
	hold := enginetest.TransactionOperation(t, holder)
	if _, err := e.Execute(hold, roleAttributeAlter(d.Name, parser.RoleOptions{}), 0, nil); err != nil {
		t.Fatal(err)
	}
	hold.Done()
	base := e.RoleStore.Published
	private := holder.Transactions.Catalog
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		op, err := waiter.Acquire(ctx)
		if err == nil {
			_, err = e.Execute(op, renameStatement(d.Name, "rename_wait_next"), 0, nil)
			op.Done()
		}
		result <- err
	}()
	enginetest.LockPending(t, e, 1)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-contextDone(t):
		t.Fatal("canceled rename did not release wait")
	}
	enginetest.LockPending(t, e, 0)
	if e.RoleStore.Published != base || holder.Transactions.Catalog != private || waiter.Transactions.Catalog != nil {
		t.Fatal("canceled waiter changed committed/private predecessor")
	}
	if enginetest.TransactionEnd(t, holder, ref, false).Committed {
		t.Fatal("holder rollback committed")
	}
	enginetest.LockEmpty(t, e)
}

// contextDone supplies a bounded join without imposing a delay on success.
func contextDone(t *testing.T) <-chan struct{} {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx.Done()
}
