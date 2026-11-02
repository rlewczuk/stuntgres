package rolecmds_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

func passwordCheckEngine() *core.Engine {
	return engine.NewWithSettings([]settings.Definition{settings.PasswordCheckDefinition()}, "passwordcheck", false)
}

func TestPasswordCheckPublication(t *testing.T) {
	e := passwordCheckEngine()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	create := func(name, password string) authorization.RoleDescriptor {
		return roleAttributeCreate(t, e, op, name, true, parser.RoleOptions{Present: parser.RolePassword | parser.RoleConnectionLimit, Password: password, ConnectionLimit: 3})
	}
	d := create("pc_stored", "Abcdef12")
	assertPasswordKeys(t, d.Password, "Abcdef12")
	without := roleAttributeCreate(t, e, op, "pc_omitted_create", false, parser.RoleOptions{})
	if without.Password != "" { t.Fatal("omitted CREATE password was not NULL") }
	enginetest.RoleExecute(t, e, op, "CREATE ROLE pc_parent")
	enginetest.RoleExecute(t, e, op, "GRANT pc_parent TO pc_stored")
	enginetest.RoleExecute(t, e, op, "COMMENT ON ROLE pc_stored IS 'kept'")
	old := e.Roles()
	oldDescriptor, _ := old.Lookup("pc_stored")
	edges := old.Memberships()
	for _, password := range []string{"short", "pc_stored9!", "abcdefgh", "12345678", fmt.Sprintf("md5%x", md5.Sum([]byte("pc_storedpc_stored")))} {
		roleAttributeFailure(t, e, op, roleAttributeAlter("pc_stored", parser.RoleOptions{Present: parser.RolePassword | parser.RoleLogin | parser.RoleConnectionLimit, Password: password, Login: false, ConnectionLimit: 0}), "22023")
		got, _ := e.Roles().Lookup("pc_stored")
		if got != oldDescriptor || !reflect.DeepEqual(edges, e.Roles().Memberships()) || len(op.DetachNotices()) != 0 { t.Fatal("failed mixed ALTER mutated hidden credential/attributes/edges/notices") }
	}
	roleAttributeFailure(t, e, op, enginetest.Statement(t, "CREATE ROLE pc_rejected PASSWORD 'short' IN ROLE pc_parent"), "22023")
	if _, exists := e.Roles().Lookup("pc_rejected"); exists { t.Fatal("failed CREATE published role") }
	created := create("pc_rejected", "Abcdef12")
	for _, edge := range e.Roles().Memberships() {
		if edge.Member == created.Ref || edge.Role == created.Ref { t.Fatal("failed CREATE retained an initial membership", edge) }
	}
	// Omitted PASSWORD preserves exact bytes, not merely successful admission.
	roleAttributeApply(t, e, op, roleAttributeAlter("pc_stored", parser.RoleOptions{Present: parser.RoleConnectionLimit, ConnectionLimit: 2}))
	got, _ := e.Roles().Lookup("pc_stored")
	if got.Password != d.Password { t.Fatal("omission changed credential") }
	for _, encoded := range []string{fmt.Sprintf("md5%x", md5.Sum([]byte("xpc_stored"))), passwordExpectedSecret("x", []byte("0123456789abcdef"), 4096)} {
		enginetest.RoleExecute(t, e, op, "SET passwordcheck.min_password_length=64")
		roleAttributeApply(t, e, op, roleAttributeAlter("pc_stored", parser.RoleOptions{Present: parser.RolePassword, Password: encoded}))
		got, _ = e.Roles().Lookup("pc_stored")
		if got.Password != encoded { t.Fatal("weak unequal prehash not stored exactly") }
		op.DetachNotices()
	}
	for _, empty := range []string{fmt.Sprintf("md5%x", md5.Sum([]byte("pc_stored"))), passwordExpectedSecret("", []byte("0123456789abcdef"), 4096)} {
		roleAttributeApply(t, e, op, roleAttributeAlter("pc_stored", parser.RoleOptions{Present: parser.RolePassword, Password: passwordExpectedSecret("x", []byte("0123456789abcdef"), 4096)}))
		roleAttributeApply(t, e, op, roleAttributeAlter("pc_stored", parser.RoleOptions{Present: parser.RolePassword, Password: empty}))
		got, _ = e.Roles().Lookup("pc_stored")
		if got.Password != "" { t.Fatal("encoded empty did not clear exact credential") }
		notices := op.DetachNotices()
		if len(notices) != 1 || notices[0].Code != "00000" || notices[0].Message != "empty string is not a valid password, clearing password" { t.Fatal("encoded empty notice", notices) }
	}
	roleAttributeApply(t, e, op, roleAttributeAlter("pc_stored", parser.RoleOptions{Present: parser.RolePassword, Password: passwordExpectedSecret("x", []byte("0123456789abcdef"), 4096)}))
	roleAttributeApply(t, e, op, roleAttributeAlter("pc_stored", parser.RoleOptions{Present: parser.RolePassword, PasswordNull: true}))
	got, _ = e.Roles().Lookup("pc_stored")
	if got.Password != "" || len(op.DetachNotices()) != 0 { t.Fatal("NULL did not bypass and clear") }
	enginetest.RoleExecute(t, e, op, "RESET passwordcheck.min_password_length")
	roleAttributeApply(t, e, op, roleAttributeAlter("pc_stored", parser.RoleOptions{Present: parser.RolePassword, Password: "Replacement9!"}))
	got, _ = e.Roles().Lookup("pc_stored")
	assertPasswordKeys(t, got.Password, "Replacement9!")
	retained, _ := old.Lookup("pc_stored")
	if retained != oldDescriptor || !reflect.DeepEqual(edges, old.Memberships()) { t.Fatal("immutable RoleRead changed") }
	enginetest.RoleExecute(t, e, op, "SET passwordcheck.min_password_length=12")
	passwordCheckControlsPrivate(t, e, op, got)
}

func passwordCheckControlsPrivate(t *testing.T, e *core.Engine, op *session.Operation, want authorization.RoleDescriptor) {
	t.Helper()
	enginetest.Run(t, e, "CREATE TABLE pc_sentinel(id INT,value TEXT)")
	enginetest.Run(t, e, "INSERT INTO pc_sentinel VALUES(7,'kept')")
	enginetest.Run(t, e, "CREATE EXTENSION unaccent")
	root := e.RoleStore.Published
	read := e.Roles()
	edges := read.Memberships()
	var dump bytes.Buffer
	if err := engine.Dump(op.Context(), e, &dump); err != nil { t.Fatal(err) }
	if strings.Contains(dump.String(), want.Password) || strings.Contains(dump.String(), "passwordcheck") { t.Fatal("serialized credential/activation") }
	check := func() {
		got, _ := e.Roles().Lookup(want.Name)
		prior, _ := read.Lookup(want.Name)
		if got != want || prior != want || e.RoleStore.Published != root || !reflect.DeepEqual(e.Roles().Memberships(), edges) { t.Fatal("native controls changed exact shared credentials/identity/edges") }
		if _, active := e.Settings.Definitions()[settings.PasswordCheckMinimum]; !active { t.Fatal("controls lost startup activation") }
		store := op.EffectiveSettings()
		store.Mu.Lock()
		minimum := store.TypedValue(settings.PasswordCheckMinimum).Number
		store.Mu.Unlock()
		if minimum != 12 { t.Fatal("native controls changed session threshold", minimum) }
		roleAttributeFailure(t, e, op, roleAttributeAlter(want.Name, parser.RoleOptions{Present: parser.RolePassword, Password: "Abcdef12"}), "22023")
	}
	if err := engine.Reset(op.Context(), e); err != nil { t.Fatal(err) }
	check()
	if err := engine.Restore(op.Context(), e, strings.NewReader(dump.String())); err != nil { t.Fatal(err) }
	check()
	for _, suffix := range []string{"CREATE SEQUENCE pc_invalid CACHE 0;", "CREATE ROLE pc_forbidden;"} {
		catalog := e.Catalog.State
		if err := engine.Restore(op.Context(), e, strings.NewReader(dump.String()+suffix)); err == nil { t.Fatal("invalid restore succeeded") }
		check()
		if e.Catalog.State != catalog { t.Fatal("rejected restore changed provider/catalog root") }
	}
}

type passwordCheckCancelContext struct {
	context.Context
	checks int
	cancel context.CancelFunc
}
func (c *passwordCheckCancelContext) Err() error {
	c.checks++
	if c.checks == 100 { c.cancel() }
	return c.Context.Err()
}

func TestPasswordCheckPublicationCancellation(t *testing.T) {
	for _, final := range []bool{false, true} {
		e := passwordCheckEngine()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		d := roleAttributeCreate(t, e, op, "pc_cancel", true, parser.RoleOptions{Present: parser.RolePassword, Password: "Abcdef12"})
		root, next, revision := e.RoleStore.Published, e.RoleStore.NextPrincipal(), e.RoleStore.NextRevision()
		ctx, cancel := context.WithCancel(op.Context())
		var observed func() bool
		password := strings.Replace(passwordExpectedSecret("x", []byte("0123456789abcdef"), 1), "$4096:", "$2147483647:", 1)
		if final {
			fence := &identityRolePublicationContext{Context: ctx, session: op.Session, cancel: cancel}
			op.Ctx = fence
			observed = func() bool { return fence.observed }
			password = "Replacement9!"
		} else {
			fence := &passwordCheckCancelContext{Context: ctx, cancel: cancel}
			op.Ctx = fence
			observed = func() bool { return fence.checks >= 100 }
		}
		_, err := rolecmds.ExecuteRoleAttributes(e, &core.StatementScope{Op: op}, roleAttributeAlter("pc_cancel", parser.RoleOptions{Present: parser.RolePassword | parser.RoleLogin, Password: password}), 0)
		cancel()
		got, _ := e.Roles().Lookup("pc_cancel")
		if !errors.Is(err, context.Canceled) || !observed() || got != d || e.RoleStore.Published != root || e.RoleStore.NextPrincipal() != next || e.RoleStore.NextRevision() != revision || len(op.DetachNotices()) != 0 { t.Fatal("cancellation published hidden verifier or attributes", final, err) }
		done()
	}
}

func TestPasswordCheckPublicationFences(t *testing.T) {
	for _, kind := range []string{"authority", "revision", "allocator"} {
		t.Run(kind, func(t *testing.T) {
			e := passwordCheckEngine()
			admin, closeAdmin := enginetest.RoleOperation(t, e, "postgres")
			defer closeAdmin()
			roleAttributeCreate(t, e, admin, "pc_actor", true, parser.RoleOptions{Present: parser.RoleCreateRole | parser.RoleSuperuser, CreateRole: true, Superuser: true})
			guarded := roleAttributeCreate(t, e, admin, "pc_guarded", true, parser.RoleOptions{Present: parser.RolePassword | parser.RoleConnectionLimit, Password: "Abcdef12", ConnectionLimit: 3})
			assertPasswordKeys(t, guarded.Password, "Abcdef12")
			old := e.Roles()
			actor, closeActor := enginetest.RoleOperation(t, e, "pc_actor")
			defer closeActor()
			original := rand.Reader
			defer func() { rand.Reader = original }()
			next, revision := e.RoleStore.NextPrincipal(), e.RoleStore.NextRevision()
			expected := e.RoleStore.Published
			rand.Reader = rolePasswordReader(func(p []byte) (int, error) {
				if !e.Mu.TryLock() { t.Fatal("salt source under engine lock") }; e.Mu.Unlock()
				if !actor.Session.Mu.TryLock() { t.Fatal("salt source under session lock") }; actor.Session.Mu.Unlock()
				store := actor.EffectiveSettings()
				if !store.Mu.TryLock() { t.Fatal("salt source under settings lock") }; store.Mu.Unlock()
				switch kind {
				case "authority":
					enginetest.RoleExecute(t, e, admin, "DROP ROLE pc_actor")
					roleAttributeCreate(t, e, admin, "pc_actor", true, parser.RoleOptions{Present: parser.RoleCreateRole | parser.RoleSuperuser, CreateRole: true, Superuser: true})
					expected = e.RoleStore.Published
				case "revision": e.RoleStore.InstallRevisionFrontierLocked(^uint64(0))
				case "allocator": e.RoleStore.InstallPrincipalFrontierLocked(^uint64(0))
				}
				for i := range p { p[i] = byte(i) }
				return len(p), nil
			})
			var stmt parser.Statement = roleAttributeAlter("pc_guarded", parser.RoleOptions{Present: parser.RolePassword | parser.RoleLogin | parser.RoleConnectionLimit, Password: "Replacement9!", Login: false, ConnectionLimit: 0})
			if kind == "allocator" {
				stmt = &parser.CreateRole{Name: parser.Identifier{Text: "pc_unpublished"}, Options: parser.RoleOptions{Present: parser.RolePassword, Password: "Abcdef12"}}
			}
			_, err := rolecmds.ExecuteRoleAttributes(e, &core.StatementScope{Op: actor}, stmt, 0)
			if err == nil || e.RoleStore.Published != expected { t.Fatal("stale preparation published", kind, err) }
			if _, exists := e.Roles().Lookup("pc_unpublished"); exists { t.Fatal("failed candidate leaked role") }
			current, _ := e.Roles().Lookup("pc_guarded")
			retained, _ := old.Lookup("pc_guarded")
			if current != guarded || retained != guarded || len(actor.DetachNotices()) != 0 { t.Fatal("publication fence changed hidden verifier, attributes or old snapshot") }
			if kind != "authority" { e.RoleStore.InstallPrincipalFrontierLocked(next); e.RoleStore.InstallRevisionFrontierLocked(revision) }
		})
	}
}
