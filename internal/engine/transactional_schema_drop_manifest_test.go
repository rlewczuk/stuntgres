package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func captureSchemaDropUnit(t *testing.T, e *Engine, op *session.Operation, stmt *parser.DropSchema) ddl.SchemaDropManifest {
	t.Helper()
	manifest, err := func() (ddl.SchemaDropManifest, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			return ddl.SchemaDropManifest{}, err
		}
		return ddl.CaptureSchemaDrop(ddlHost{e: e, op: op}, auth, stmt)
	}()
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func constructSchemaDropUnit(e *Engine, op *session.Operation, stmt *parser.DropSchema, manifest ddl.SchemaDropManifest) (*CatalogStage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stageSchemaDropLocked(op, stmt, manifest)
}

func TestTransactionalSchemaDropManifest(t *testing.T) {
	for _, change := range []string{"unchanged", "unrelated", "all_absent", "absence_appears", "rename", "replacement", "deleted", "epoch", "foreign_domain", "canceled"} {
		t.Run(change, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE SCHEMA manifest_held")
			held := schemaRef(t, e, "manifest_held")
			op, done := roleOperation(t, e, "postgres")
			defer done()
			stmt := statement(t, "DROP SCHEMA IF EXISTS manifest_held, manifest_absent, manifest_held CASCADE").(*parser.DropSchema)
			if change == "epoch" || change == "foreign_domain" {
				stmt = statement(t, "DROP SCHEMA IF EXISTS manifest_absent").(*parser.DropSchema)
			}
			if change == "all_absent" {
				stmt = statement(t, "DROP SCHEMA IF EXISTS manifest_absent, manifest_absent CASCADE").(*parser.DropSchema)
			}
			manifest := captureSchemaDropUnit(t, e, op, stmt)
			if got := op.DetachNotices(); len(got) != 0 {
				t.Fatal("capture emitted conditional notices", got)
			}
			if change != "epoch" && change != "foreign_domain" && change != "all_absent" {
				want := []ddl.SchemaDropTarget{{Name: "manifest_held", Ref: held}, {Name: "manifest_absent"}, {Name: "manifest_held", Ref: held}}
				if !reflect.DeepEqual(manifest.Targets, want) {
					t.Fatalf("captured target identities/absences: %#v want %#v", manifest.Targets, want)
				}
			}
			switch change {
			case "unrelated":
				run(t, e, "CREATE SCHEMA manifest_unrelated")
			case "absence_appears":
				run(t, e, "CREATE SCHEMA manifest_absent")
			case "rename":
				run(t, e, "ALTER SCHEMA manifest_held RENAME TO manifest_moved")
			case "replacement":
				run(t, e, "ALTER SCHEMA manifest_held RENAME TO manifest_moved")
				run(t, e, "CREATE SCHEMA manifest_held")
				if schemaRef(t, e, "manifest_held") == held {
					t.Fatal("replacement reused admitted identity")
				}
			case "deleted":
				run(t, e, "DROP SCHEMA manifest_held")
			case "epoch":
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "foreign_domain":
				manifest.Domain = New().catalog.Domain
			case "canceled":
				ctx, cancel := context.WithCancel(op.Context())
				cancel()
				op.Ctx = ctx
			}
			before := e.CatalogRead()
			stage, err := constructSchemaDropUnit(e, op, stmt, manifest)
			if stage != nil {
				defer e.DiscardCatalogStage(stage)
			}
			if e.CatalogRead().State() != before.State() {
				t.Fatal("stage construction changed the published catalog")
			}
			if change == "all_absent" {
				if err != nil || stage != nil || schemaRef(t, e, "manifest_held") != held {
					t.Fatal("conditional absence changed an existing namespace", stage, err)
				}
				notice := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "manifest_absent" does not exist, skipping`}
				if got := op.DetachNotices(); !reflect.DeepEqual(got, []sqlstate.Error{notice, notice}) {
					t.Fatal("duplicate conditional absence notices", got)
				}
				return
			}
			if change != "unchanged" && change != "unrelated" {
				want := ids.ErrCatalogReplaced
				if change == "canceled" {
					want = context.Canceled
				}
				if !errors.Is(err, want) || stage != nil {
					t.Fatalf("changed admission-to-stage binding accepted: stage=%v err=%v want %v", stage, err, want)
				}
				if got := op.DetachNotices(); len(got) != 0 {
					t.Fatal("rejected manifest emitted removal notices", got)
				}
				return
			}
			if err != nil || stage == nil {
				t.Fatal("unchanged admitted manifest rejected", err)
			}
			if stage.RemovalCount() != 1 || !stage.Removed(held.Address) {
				t.Fatal("drop stage did not remove exactly the admitted namespace")
			}
			if change == "unrelated" {
				other := schemaRef(t, e, "manifest_unrelated")
				if stage.Removed(other.Address) || e.stageRead(stage).Get(other.Address) != before.Get(other.Address) {
					t.Fatal("unrelated namespace was removed or rebound")
				}
			}
			want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "manifest_absent" does not exist, skipping`}}
			if got := op.DetachNotices(); !reflect.DeepEqual(got, want) {
				t.Fatalf("conditional notices %#v want %#v", got, want)
			}
		})
	}
	t.Run("current_owner", func(t *testing.T) {
		e := New()
		for _, q := range []string{"CREATE ROLE manifest_actor LOGIN", "CREATE ROLE manifest_other", "CREATE SCHEMA manifest_held AUTHORIZATION manifest_actor"} {
			run(t, e, q)
		}
		op, done := roleOperation(t, e, "manifest_actor")
		defer done()
		stmt := statement(t, "DROP SCHEMA manifest_held").(*parser.DropSchema)
		manifest := captureSchemaDropUnit(t, e, op, stmt)
		run(t, e, "ALTER SCHEMA manifest_held OWNER TO manifest_other")
		before := e.CatalogRead()
		stage, err := constructSchemaDropUnit(e, op, stmt, manifest)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "must be owner of schema manifest_held" || stage != nil || e.CatalogRead().State() != before.State() {
			t.Fatal("drop lost current namespace ownership", stage, err)
		}
	})
	t.Run("private_namespace", func(t *testing.T) {
		e := New()
		owner := sessionStart(t, e, nil)
		ref := transactionBegin(t, owner)
		sessionSQL(t, owner, "CREATE SCHEMA manifest_private")
		op := transactionOperation(t, owner)
		defer func() { op.Done(); transactionEnd(t, owner, ref, false) }()
		stmt := statement(t, "DROP SCHEMA manifest_private").(*parser.DropSchema)
		manifest := captureSchemaDropUnit(t, e, op, stmt)
		if len(manifest.Targets) != 1 || manifest.Targets[0].Ref == (ids.ObjectRef{}) || e.CatalogRead().Get(manifest.Targets[0].Ref.Address) != nil {
			t.Fatal("private namespace was not captured privately")
		}
		stage, err := constructSchemaDropUnit(e, op, stmt, manifest)
		if err != nil || stage == nil {
			t.Fatal("private drop stage", err)
		}
		defer e.DiscardCatalogStage(stage)
		if !stage.Removed(manifest.Targets[0].Ref.Address) || stage.RemovalCount() != 1 {
			t.Fatal("private admitted namespace not retired")
		}
	})
}
