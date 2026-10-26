package schemacmds_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestCommentMixedOwnerRoundtrip(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		for _, tc := range []struct{ name, table, a, b string }{{"table only", "table text", "", ""}, {"columns only", "", "first", "second"}, {"table and columns", "table text", "first", "second"}} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				e := engine.New()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				for _, q := range []string{
					"CREATE USER table_owner", "CREATE USER schema_owner", "CREATE SCHEMA s AUTHORIZATION table_owner",
					"SET SESSION AUTHORIZATION table_owner", "CREATE TABLE s.t(a INT,b TEXT)", "INSERT INTO s.t VALUES(7,'sentinel')",
					"RESET SESSION AUTHORIZATION", "ALTER SCHEMA s OWNER TO schema_owner",
					"COMMENT ON ROLE table_owner IS 'shared table owner'", "COMMENT ON ROLE schema_owner IS 'shared schema owner'",
				} {
					enginetest.RoleExecute(t, e, op, q)
				}
				for _, c := range []struct{ target, text string }{{"TABLE s.t", tc.table}, {"COLUMN s.t.a", tc.a}, {"COLUMN s.t.b", tc.b}} {
					if c.text == "" {
						continue
					}
					stmt := enginetest.Statement(t, "COMMENT ON "+c.target+" IS '"+c.text+"'")
					if prepared {
						if _, err := e.Describe(op, stmt); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := e.Execute(op, stmt, 0, nil); err != nil {
						t.Fatal(err)
					}
				}
				canonical := commentDump(t, e)
				identity := op.RoleState()
				settings := op.Session.Settings
				rolesPublication := e.RoleStore.Published
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
				if err := engine.Restore(context.Background(), e, strings.NewReader(canonical)); err != nil {
					t.Fatal("commented mixed-owner dump cannot replay", err, canonical)
				}
				if got := commentDump(t, e); got != canonical {
					t.Fatal("mixed-owner canonical stream changed", got, canonical)
				}
				if op.RoleState() != identity || op.Session.Settings != settings || e.RoleStore.Published != rolesPublication {
					t.Fatal("Restore changed live identity/settings/shared authority")
				}
				tableRole, _ := e.Roles().Lookup("table_owner")
				schemaRole, _ := e.Roles().Lookup("schema_owner")
				ns := schemaRef(t, e, "s")
				ref := enginetest.RoleTableRef(t, e, "s.t")
				nsDesc := e.CatalogRead().MustNamespace(ns.Address)
				tableDesc := e.CatalogRead().MustRelation(ref.Address)
				if nsDesc.Owner != schemaRole.Ref || tableDesc.Owner != tableRole.Ref || nsDesc.ACL.Explicit || tableDesc.Security.ACL.Explicit {
					t.Fatal("restore changed independent owners or synthesized grants")
				}
				if tableRole.Comment != "shared table owner" || schemaRole.Comment != "shared schema owner" {
					t.Fatal("restore changed shared text", tableRole, schemaRole)
				}
				commentWant(t, e, e.CatalogRead(), ref, tc.table)
				ref.Address.SubID = 1
				commentWant(t, e, e.CatalogRead(), ref, tc.a)
				ref.Address.SubID = 2
				commentWant(t, e, e.CatalogRead(), ref, tc.b)
				denied, closeDenied := enginetest.RoleOperation(t, e, "table_owner")
				defer closeDenied()
				commentFailure(t, e, denied, "COMMENT ON TABLE s.t IS 'public denial'", "42501", "permission denied for schema s", "")
				_, err := e.Execute(denied, enginetest.Statement(t, "SELECT a FROM s.t"), 0, nil)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for schema s" || diagnostic.Position != "15" {
					t.Fatal("public SELECT lost schema-USAGE enforcement", err)
				}
			})
		}
	}
}

func TestCommentQualifiedDiagnostics(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER denied", "CREATE SCHEMA private", "CREATE TABLE private.t(v INT)", "INSERT INTO private.t VALUES(7)", "COMMENT ON TABLE private.t IS 'old'", "COMMENT ON COLUMN private.t.v IS 'old column'"} {
		enginetest.RoleExecute(t, e, admin, q)
	}
	denied, closeDenied := enginetest.RoleOperation(t, e, "denied")
	defer closeDenied()
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ q, code, message string }{
			{"COMMENT ON TABLE missing_schema.t IS NULL", "3F000", `schema "missing_schema" does not exist`},
			{"COMMENT ON COLUMN missing_schema.t.v IS NULL", "3F000", `schema "missing_schema" does not exist`},
			{"COMMENT ON TABLE private.t IS NULL", "42501", "permission denied for schema private"},
			{"COMMENT ON COLUMN private.t.missing IS NULL", "42501", "permission denied for schema private"},
		} {
			stmt := enginetest.Statement(t, tc.q)
			if prepared {
				info, err := e.Describe(denied, stmt)
				if err != nil {
					t.Fatal("semantic failure occurred at Prepare", err)
				}
				if !reflect.DeepEqual(info, core.Description{}) {
					t.Fatal("utility Prepare metadata", info)
				}
			}
			before := e.CatalogRead()
			rolesPublication := e.RoleStore.Published
			storage := e.Storage.Relations[enginetest.RoleTableRef(t, e, "private.t").Address]
			_, err := e.Execute(denied, stmt, 0, nil)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			if !errors.As(err, &got) || *got != want {
				t.Errorf("prepared=%v %s: full diagnostic %#v want %#v", prepared, tc.q, got, want)
			}
			if before.State() != e.CatalogRead().State() || rolesPublication != e.RoleStore.Published || storage != e.Storage.Relations[enginetest.RoleTableRef(t, e, "private.t").Address] {
				t.Fatal("failed comment changed catalog, roles or data")
			}
		}
	}
	commentWant(t, e, e.CatalogRead(), enginetest.RoleTableRef(t, e, "private.t"), "old")
	column := enginetest.RoleTableRef(t, e, "private.t")
	column.Address.SubID = 1
	commentWant(t, e, e.CatalogRead(), column, "old column")
}

func TestCommentPublishingWaiters(t *testing.T) {
	for _, tc := range []struct{ name, first, second, table, a, b string }{
		{"table replacement", "COMMENT ON TABLE t IS 'first'", "COMMENT ON TABLE t IS 'second'", "second", "", ""},
		{"independent columns", "COMMENT ON COLUMN t.a IS 'first'", "COMMENT ON COLUMN t.b IS 'second'", "", "first", "second"},
		{"table and column", "COMMENT ON TABLE t IS 'first'", "COMMENT ON COLUMN t.a IS 'second'", "first", "second", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			first := enginetest.StartSession(t, e, nil)
			second := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, holder, "CREATE TABLE t(a INT,b INT)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			original := e.CatalogRead().MustRelation(ref.Address)
			tx := enginetest.TransactionBegin(t, holder)
			enginetest.SessionSQL(t, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			firstDone := enginetest.LockAsync(e, first, context.Background(), tc.first, false)
			enginetest.LockPending(t, e, 1)
			secondDone := enginetest.LockAsync(e, second, context.Background(), tc.second, false)
			enginetest.LockPending(t, e, 2)
			enginetest.TransactionEnd(t, holder, tx, false)
			enginetest.LockAwait(t, firstDone, nil)
			enginetest.LockAwait(t, secondDone, nil)
			commentWant(t, e, e.CatalogRead(), ref, tc.table)
			ref.Address.SubID = 1
			commentWant(t, e, e.CatalogRead(), ref, tc.a)
			ref.Address.SubID = 2
			commentWant(t, e, e.CatalogRead(), ref, tc.b)
			current := e.CatalogRead().MustRelation(original.Address())
			if current.Owner != original.Owner || !reflect.DeepEqual(current.Attributes, original.Attributes) || current.Address() != original.Address() {
				t.Fatal("serialized comments changed object authority/column identities")
			}
			enginetest.SessionSQL(t, first, "INSERT INTO t VALUES(7,8)")
			enginetest.SessionSQL(t, second, "INSERT INTO t VALUES(9,10)")
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestCommentRestoreAuthorityIsolation(t *testing.T) {
	source := engine.New()
	enginetest.Run(t, source, "CREATE USER ordinary")
	for _, tc := range []struct {
		name         string
		engine       *core.Engine
		actor, query string
	}{
		{"public operation", source, "postgres", "COMMENT ON SCHEMA public IS 'forbidden'"},
		{"non-superuser private authenticator", source.RestoreStage(), "ordinary", "COMMENT ON SCHEMA public IS 'forbidden'"},
		{"shared role excluded", source.RestoreStage(), "postgres", "COMMENT ON ROLE ordinary IS 'forbidden'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op, done := enginetest.RoleOperation(t, tc.engine, tc.actor)
			defer done()
			before := tc.engine.CatalogRead()
			rolesPublication := tc.engine.RoleStore.Published
			identity := op.RoleState()
			core.OperationReplay(op).CommentAuthority = true
			_, err := tc.engine.Execute(op, enginetest.Statement(t, tc.query), 0, nil)
			core.OperationReplay(op).CommentAuthority = false
			var diagnostic *sqlstate.Error
			if err == nil || errors.As(err, &diagnostic) {
				t.Fatal("invalid private marker accepted or fabricated SQL diagnostic", err)
			}
			if tc.engine.CatalogRead().State() != before.State() || tc.engine.RoleStore.Published != rolesPublication || op.RoleState() != identity {
				t.Fatal("invalid private marker changed authority/metadata")
			}
		})
	}
}
