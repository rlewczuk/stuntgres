package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestNoticesFilter(t *testing.T) {
	thresholds := []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error", "debug"}
	ranks := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 3}
	witness := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "field witness", Detail: "facts", Hint: "advice", Position: "3", InternalPosition: "2", InternalQuery: "x+", Context: "context", Schema: "s", Table: "t", Column: "c", DataType: "d", Constraint: "k"}
	for i, threshold := range thresholds {
		t.Run(threshold, func(t *testing.T) {
			normalized, notice, err := settings.ClientMinMessages.Normalize(threshold)
			if err != nil || notice != nil {
				t.Fatal(normalized, notice, err)
			}
			level, _ := messageLevel(normalized)
			for emitted := 0; emitted <= 9; emitted++ {
				op := &Operation{noticeThreshold: level}
				op.emitNotice(noticeLevel(emitted), witness)
				got := op.DetachNotices()
				want := emitted < 9 && (emitted == 6 || emitted >= ranks[i])
				if (len(got) == 1) != want {
					t.Fatalf("level %d got %v want delivery %v", emitted, got, want)
				}
				if want {
					if got[0] != witness {
						t.Fatal("fields changed")
					}
					got[0].Detail = "mutated"
					if witness.Detail != "facts" {
						t.Fatal("payload aliases")
					}
				}
				if op.DetachNotices() != nil {
					t.Fatal("not detached")
				}
			}
		})
	}
}
func TestNoticesCommands(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)")
	run(t, e, "INSERT INTO notice_rows VALUES(7,'kept')")
	before := e.CatalogRead()
	e.mu.Lock()
	ref, err := e.resolveReference(catalogAuthorization(e, before), statement(t, "SELECT * FROM notice_rows").(*parser.Select).From.(*parser.TableReference).Name)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	r, err := e.Execute(op, statement(t, "CREATE TABLE IF NOT EXISTS notice_rows(id INT,id TEXT)"), 0, nil)
	if err != nil || r.Count != 0 {
		t.Fatal(r, err)
	}
	want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}}
	if got := op.DetachNotices(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%#v", got)
	}
	after := e.CatalogRead()
	d, _ := before.Descriptor(ref.Address)
	actual, _ := after.Descriptor(ref.Address)
	if d != actual {
		t.Fatal("descriptor changed")
	}
	got := run(t, e, "SELECT * FROM notice_rows")
	if !reflect.DeepEqual(tuples(got), [][]driver.Value{{int64(7), "kept"}}) {
		t.Fatal(tuples(got))
	}
	_, err = e.Execute(op, statement(t, "CREATE TABLE IF NOT EXISTS fresh(id INT,id TEXT)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42701", Message: `column "id" specified more than once`}) {
		t.Fatal(err)
	}
}

func TestNoticesPublicationConflict(t *testing.T) {
	e := New()
	var winner catalog.Read
	e.catalog.Providers[ids.RelationClassID] = &publicationConflictProvider{Provider: e.catalog.Providers[ids.RelationClassID], publishWinner: func(d catalog.Descriptor) {
		run(t, e, "CREATE TABLE conflict_rows(kept TEXT NOT NULL)")
		run(t, e, "INSERT INTO conflict_rows VALUES('winner')")
		winner = e.CatalogRead()
	}}
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	r, err := e.Execute(op, statement(t, "CREATE TABLE IF NOT EXISTS conflict_rows(lost INT)"), 0, nil)
	if err != nil || r.Count != 0 {
		t.Fatal(r, err)
	}
	want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "conflict_rows" already exists, skipping`}}
	if got := op.DetachNotices(); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if e.CatalogRead().State() != winner.State() {
		t.Fatal("winner catalog replaced")
	}
	got := run(t, e, "SELECT kept FROM conflict_rows")
	if len(got.Rows.Schema()) != 1 || got.Rows.Schema()[0].Usage.Type.ID() != sqltype.Text || got.Rows.Schema()[0].Nullable || !reflect.DeepEqual(tuples(got), [][]driver.Value{{"winner"}}) {
		t.Fatal(got, tuples(got))
	}
}

func TestNoticesPublicationConflictCanceled(t *testing.T) {
	for _, clause := range []string{"", "IF NOT EXISTS "} {
		t.Run(clause, func(t *testing.T) {
			e := New()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var loser ids.ObjectAddress
			var winner catalog.Read
			e.catalog.Providers[ids.RelationClassID] = &publicationConflictProvider{
				Provider: e.catalog.Providers[ids.RelationClassID],
				publishWinner: func(d catalog.Descriptor) {
					loser = d.Address()
					run(t, e, "CREATE TABLE conflict_rows(kept TEXT NOT NULL)")
					run(t, e, "INSERT INTO conflict_rows VALUES('winner')")
					winner = e.CatalogRead()
					cancel()
				},
			}
			op, cleanup, err := testOperation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			result, err := e.Execute(op, statement(t, "CREATE TABLE "+clause+"conflict_rows(lost INT)"), 0, nil)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("canceled losing CREATE returned %v, want context.Canceled", err)
			}
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) {
				t.Errorf("ordinary cancellation became SQL diagnostic: %#v", diagnostic)
			}
			if result.Count != 0 || result.Rows != nil {
				t.Errorf("losing CREATE exposed result: %#v", result)
			}
			if notices := op.DetachNotices(); len(notices) != 0 {
				t.Errorf("canceled losing CREATE emitted notices: %#v", notices)
			}
			after := e.CatalogRead()
			if after.State() != winner.State() {
				t.Fatal("losing CREATE replaced winner catalog")
			}
			if _, exists := after.Descriptor(loser); exists || e.storage.Relations[loser] != nil {
				t.Fatal("losing CREATE published descriptor or storage")
			}
			got := run(t, e, "SELECT kept FROM conflict_rows")
			if len(got.Rows.Schema()) != 1 || got.Rows.Schema()[0].Usage.Type.ID() != sqltype.Text || got.Rows.Schema()[0].Nullable ||
				!reflect.DeepEqual(tuples(got), [][]driver.Value{{"winner"}}) {
				t.Fatal("winner definition/data changed", got, tuples(got))
			}
		})
	}
}

// Pause after observing a healthy context, before the caller acts on that
// observation. The real operation context still owns cancellation and cleanup.
type noticeConflictContext struct {
	context.Context
	pause func()
}

func (c *noticeConflictContext) Err() error {
	err := c.Context.Err()
	if pause := c.pause; pause != nil {
		c.pause = nil
		pause()
	}
	return err
}

func TestNoticesPublicationConflictCanceledBeforeSkip(t *testing.T) {
	e := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	op, cleanup, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	observed := make(chan struct{})
	release := make(chan struct{})
	wrapped := &noticeConflictContext{Context: op.ctx}
	op.ctx = wrapped
	var loser ids.ObjectAddress
	var winner catalog.Read
	e.catalog.Providers[ids.RelationClassID] = &publicationConflictProvider{
		Provider: e.catalog.Providers[ids.RelationClassID],
		publishWinner: func(d catalog.Descriptor) {
			loser = d.Address()
			run(t, e, "CREATE TABLE conflict_rows(kept TEXT NOT NULL)")
			run(t, e, "INSERT INTO conflict_rows VALUES('winner')")
			winner = e.CatalogRead()
			wrapped.pause = func() { close(observed); <-release }
		},
	}
	stmt := statement(t, "CREATE TABLE IF NOT EXISTS conflict_rows(lost INT)")
	returned := make(chan error, 1)
	go func() {
		_, err := e.Execute(op, stmt, 0, nil)
		returned <- err
	}()
	select {
	case <-observed:
	case <-ctx.Done():
		close(release)
		t.Fatal("conflict context check not reached", ctx.Err())
	}
	// Hold the coherent-state lock while cancellation overtakes the captured
	// healthy observation. The final skip decision must recheck under this lock.
	e.mu.Lock()
	cancel()
	close(release)
	e.mu.Unlock()
	select {
	case err = <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled conflict failed to join")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled skip returned %v, want context.Canceled", err)
	}
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) {
		t.Errorf("ordinary cancellation became SQL diagnostic: %#v", diagnostic)
	}
	if notices := op.DetachNotices(); len(notices) != 0 {
		t.Errorf("canceled skip emitted notices: %#v", notices)
	}
	after := e.CatalogRead()
	if after.State() != winner.State() {
		t.Fatal("losing CREATE replaced winner catalog")
	}
	if _, exists := after.Descriptor(loser); exists || e.storage.Relations[loser] != nil {
		t.Fatal("losing CREATE published descriptor or storage")
	}
	got := run(t, e, "SELECT kept FROM conflict_rows")
	if len(got.Rows.Schema()) != 1 || got.Rows.Schema()[0].Usage.Type.ID() != sqltype.Text || got.Rows.Schema()[0].Nullable ||
		!reflect.DeepEqual(tuples(got), [][]driver.Value{{"winner"}}) {
		t.Fatal("winner definition/data changed", got, tuples(got))
	}
}

func TestNoticesErrorCarriage(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE error_rows(id INT)")
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, err = e.Execute(op, statement(t, "CREATE TABLE IF NOT EXISTS error_rows(id INT)"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "CREATE TABLE error_rows(id INT)"), 0, nil)
	state(t, err, "42P07")
	want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "error_rows" already exists, skipping`}}
	if got := op.DetachNotices(); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	// Authorization failures use the same complete diagnostic value carriage.
	permission := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema public"}
	if permission.SQLState() != "42501" || permission.Error() != "stuntgres: permission denied for schema public" {
		t.Fatal(permission)
	}
}

type noticeDefaults struct{}

func (noticeDefaults) SessionDefault(_ context.Context, d SettingDefinition) (string, bool, error) {
	if d.Name == "client_min_messages" {
		return "WARNING", true, nil
	}
	if d.Name == "application_name" {
		return strings.Repeat("a", 64), true, nil
	}
	return "", false, nil
}
func TestNoticesCapturedDefaults(t *testing.T) {
	e := New()
	s := sessionStart(t, e, noticeDefaults{})
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	for _, q := range []string{"SET client_min_messages TO notice", "SET client_min_messages TO DEFAULT"} {
		if _, err = e.Execute(op, statement(t, q), 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	r, err := e.Execute(op, statement(t, "SHOW client_min_messages"), 0, nil)
	if err != nil || !reflect.DeepEqual(tuples(r), [][]driver.Value{{"warning"}}) {
		t.Fatal(r, err)
	}
	r, err = e.Execute(op, statement(t, "SHOW application_name"), 0, nil)
	if err != nil || !reflect.DeepEqual(tuples(r), [][]driver.Value{{strings.Repeat("a", 63)}}) || op.DetachNotices() != nil {
		t.Fatal(r, err)
	}
}

func TestNoticesPublicationReplacement(t *testing.T) {
	e := New()
	e.catalog.Providers[ids.RelationClassID] = &publicationConflictProvider{Provider: e.catalog.Providers[ids.RelationClassID], publishWinner: func(d catalog.Descriptor) {
		if err := e.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		run(t, e, "CREATE TABLE epoch_rows(kept TEXT)")
	}}
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, err = e.Execute(op, statement(t, "CREATE TABLE IF NOT EXISTS epoch_rows(lost INT)"), 0, nil)
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("replacement became skip", err)
	}
	if got := op.DetachNotices(); got != nil {
		t.Fatal(got)
	}
}

func TestRoleNoticesCreateAuthorizationAndSettings(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER notice_owner")
	roleExecute(t, e, op, "CREATE USER notice_reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION notice_owner")
	withPublicSchemaUnitOwner(t, e, "notice_owner", func() {
		roleExecute(t, e, op, "CREATE TABLE notice_secret(id INT)")
	})
	roleExecute(t, e, op, "INSERT INTO notice_secret VALUES(7)")
	roleExecute(t, e, op, "SET client_min_messages TO warning")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION notice_reader")
	skip := statement(t, "CREATE TABLE IF NOT EXISTS notice_secret(id INT,id TEXT)")
	var before catalog.Read
	withPublicSchemaUnitOwner(t, e, "notice_reader", func() {
		if _, err := e.Describe(op, skip); err != nil {
			t.Fatal("CREATE preparation", err)
		}
		before = e.CatalogRead()
		if result, err := e.Execute(op, skip, 0, nil); err != nil || result.Count != 0 || op.DetachNotices() != nil {
			t.Fatal("filtered authorized skip", result, err)
		}
		if e.CatalogRead().State() != before.State() {
			t.Fatal("skip published catalog changes")
		}
	})
	roleExecute(t, e, op, "SET client_min_messages TO notice")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION notice_reader")
	withPublicSchemaUnitOwner(t, e, "notice_reader", func() {
		roleExecute(t, e, op, "CREATE TABLE IF NOT EXISTS notice_secret(id INT,id TEXT)")
	})
	want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_secret" already exists, skipping`}}
	if got := op.DetachNotices(); !reflect.DeepEqual(got, want) {
		t.Fatal("switch reset notice filter", got)
	}
	namespace := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if err := e.SetObjectACL(context.Background(), namespace, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	before = e.CatalogRead()
	if _, err := e.Describe(op, skip); err != nil {
		t.Fatal("CREATE authorization is execution-time", err)
	}
	roleErrorPosition(t, e, op, "CREATE TABLE IF NOT EXISTS notice_secret(id INT,id TEXT)", "42501", "permission denied for schema public", "", "28")
	if op.DetachNotices() != nil || e.CatalogRead().State() != before.State() {
		t.Fatal("denied skip emitted notice or mutated catalog")
	}
	// Revoking USAGE removes public from the unqualified destination path.
	if err := e.SetObjectACL(context.Background(), namespace, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	before = e.CatalogRead()
	roleErrorPosition(t, e, op, "CREATE TABLE IF NOT EXISTS notice_secret(id INT,id TEXT)", "3F000", "no schema has been selected to create in", "", "28")
	if op.DetachNotices() != nil || e.CatalogRead().State() != before.State() {
		t.Fatal("unselected skip emitted notice or mutated catalog")
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	result := roleExecute(t, e, op, "SELECT id FROM notice_secret")
	if !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(7)}}) {
		t.Fatal("sentinel changed", tuples(result))
	}
}

func TestRoleNoticesConflictSkipRechecksCreateAuthorization(t *testing.T) {
	for _, usage := range []bool{true, false} {
		t.Run(fmt.Sprintf("usage=%t", usage), func(t *testing.T) {
			e := New()
			admin, adminDone := roleOperation(t, e, "postgres")
			defer adminDone()
			roleExecute(t, e, admin, "CREATE USER losing_creator")
			loser, loserDone := roleOperation(t, e, "losing_creator")
			defer loserDone()
			public := schemaRef(t, e, "public")
			descriptor, ok := e.CatalogRead().Descriptor(public.Address)
			if !ok {
				t.Fatal("public namespace missing")
			}
			prior, err := e.Roles().Descriptor(descriptor.(*catalog.Namespace).Owner)
			if err != nil {
				t.Fatal(err)
			}
			var winner catalog.Read
			e.catalog.Providers[ids.RelationClassID] = &publicationConflictProvider{
				Provider: e.catalog.Providers[ids.RelationClassID],
				publishWinner: func(catalog.Descriptor) {
					roleExecute(t, e, admin, "CREATE TABLE contested_notice(id INT)")
					roleExecute(t, e, admin, "INSERT INTO contested_notice VALUES(7)")
					view := e.CatalogRead()
					namespace := e.catalog.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
					acl := authorization.ACLValue{Explicit: true}
					if usage {
						acl.Entries = []authorization.ACLEntry{{Grantor: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}
					}
					roleExecute(t, e, admin, `ALTER SCHEMA public OWNER TO "`+strings.ReplaceAll(prior.Name, `"`, `""`)+`"`)
					if err := e.SetObjectACL(context.Background(), namespace, acl); err != nil {
						t.Fatal(err)
					}
					winner = e.CatalogRead()
				},
			}
			code, message := "3F000", "no schema has been selected to create in"
			if usage {
				code, message = "42501", "permission denied for schema public"
			}
			withPublicSchemaUnitOwner(t, e, "losing_creator", func() {
				roleErrorPosition(t, e, loser, "CREATE TABLE IF NOT EXISTS contested_notice(lost TEXT)", code, message, "", "28")
				if loser.DetachNotices() != nil || e.CatalogRead().State() != winner.State() {
					t.Fatal("unauthorized conflict became skip or changed winner")
				}
			})
			result := roleExecute(t, e, admin, "SELECT id FROM contested_notice")
			if !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(7)}}) {
				t.Fatal(tuples(result))
			}
		})
	}
}
