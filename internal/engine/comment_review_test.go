package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestCommentMixedOwnerRoundtrip(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		for _, tc := range []struct{ name, table, a, b string }{{"table only", "table text", "", ""}, {"columns only", "", "first", "second"}, {"table and columns", "table text", "first", "second"}} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				e := New()
				op, done := roleOperation(t, e, "postgres")
				defer done()
				for _, q := range []string{
					"CREATE USER table_owner", "CREATE USER schema_owner", "CREATE SCHEMA s AUTHORIZATION table_owner",
					"SET SESSION AUTHORIZATION table_owner", "CREATE TABLE s.t(a INT,b TEXT)", "INSERT INTO s.t VALUES(7,'sentinel')",
					"RESET SESSION AUTHORIZATION", "ALTER SCHEMA s OWNER TO schema_owner",
					"COMMENT ON ROLE table_owner IS 'shared table owner'", "COMMENT ON ROLE schema_owner IS 'shared schema owner'",
				} {
					roleExecute(t, e, op, q)
				}
				for _, c := range []struct{ target, text string }{{"TABLE s.t", tc.table}, {"COLUMN s.t.a", tc.a}, {"COLUMN s.t.b", tc.b}} {
					if c.text == "" {
						continue
					}
					stmt := statement(t, "COMMENT ON "+c.target+" IS '"+c.text+"'")
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
				settings := op.session.settings
				rolesPublication := e.roles
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := e.Restore(context.Background(), strings.NewReader(canonical)); err != nil {
					t.Fatal("commented mixed-owner dump cannot replay", err, canonical)
				}
				if got := commentDump(t, e); got != canonical {
					t.Fatal("mixed-owner canonical stream changed", got, canonical)
				}
				if op.RoleState() != identity || op.session.settings != settings || e.roles != rolesPublication {
					t.Fatal("Restore changed live identity/settings/shared authority")
				}
				tableRole, _ := e.Roles().Lookup("table_owner")
				schemaRole, _ := e.Roles().Lookup("schema_owner")
				ns := schemaRef(t, e, "s")
				ref := roleTableRef(t, e, "s.t")
				nsDesc := e.CatalogRead().state.descriptors[ns.Address].(*namespaceDescriptor)
				tableDesc := e.CatalogRead().state.descriptors[ref.Address].(*relationDescriptor)
				if nsDesc.owner != schemaRole.Ref || tableDesc.owner != tableRole.Ref || nsDesc.acl.Explicit || tableDesc.acl.Explicit {
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
				if got := tuples(roleExecute(t, e, op, "SELECT a FROM s.t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
					t.Fatal("lost sentinel", got)
				}
				denied, closeDenied := roleOperation(t, e, "table_owner")
				defer closeDenied()
				commentFailure(t, e, denied, "COMMENT ON TABLE s.t IS 'public denial'", "42501", "permission denied for schema s", "")
				_, err := e.Execute(denied, statement(t, "SELECT a FROM s.t"), 0, nil)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for schema s" || diagnostic.Position != "15" {
					t.Fatal("public SELECT lost schema-USAGE enforcement", err)
				}
			})
		}
	}
}

func TestCommentQualifiedDiagnostics(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER denied", "CREATE SCHEMA private", "CREATE TABLE private.t(v INT)", "INSERT INTO private.t VALUES(7)", "COMMENT ON TABLE private.t IS 'old'", "COMMENT ON COLUMN private.t.v IS 'old column'"} {
		roleExecute(t, e, admin, q)
	}
	denied, closeDenied := roleOperation(t, e, "denied")
	defer closeDenied()
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ q, code, message string }{
			{"COMMENT ON TABLE missing_schema.t IS NULL", "3F000", `schema "missing_schema" does not exist`},
			{"COMMENT ON COLUMN missing_schema.t.v IS NULL", "3F000", `schema "missing_schema" does not exist`},
			{"COMMENT ON TABLE private.t IS NULL", "42501", "permission denied for schema private"},
			{"COMMENT ON COLUMN private.t.missing IS NULL", "42501", "permission denied for schema private"},
		} {
			stmt := statement(t, tc.q)
			if prepared {
				info, err := e.Describe(denied, stmt)
				if err != nil {
					t.Fatal("semantic failure occurred at Prepare", err)
				}
				if !reflect.DeepEqual(info, StatementDescription{}) {
					t.Fatal("utility Prepare metadata", info)
				}
			}
			before := e.CatalogRead()
			rolesPublication := e.roles
			storage := e.storage[roleTableRef(t, e, "private.t").Address]
			_, err := e.Execute(denied, stmt, 0, nil)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			if !errors.As(err, &got) || *got != want {
				t.Errorf("prepared=%v %s: full diagnostic %#v want %#v", prepared, tc.q, got, want)
			}
			if before.state != e.CatalogRead().state || rolesPublication != e.roles || storage != e.storage[roleTableRef(t, e, "private.t").Address] {
				t.Fatal("failed comment changed catalog, roles or data")
			}
		}
	}
	commentWant(t, e, e.CatalogRead(), roleTableRef(t, e, "private.t"), "old")
	column := roleTableRef(t, e, "private.t")
	column.Address.SubID = 1
	commentWant(t, e, e.CatalogRead(), column, "old column")
	if got := tuples(roleExecute(t, e, admin, "SELECT v FROM private.t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal("failed comments changed sentinel", got)
	}
	for _, tc := range []struct{ q, position string }{{"SELECT v FROM private.t", "15"}, {"INSERT INTO private.t VALUES(9)", "13"}} {
		_, err := e.Execute(denied, statement(t, tc.q), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema private", Position: tc.position}
		if !errors.As(err, &got) || *got != want {
			t.Fatal("COMMENT changed DML diagnostics", got, want)
		}
	}
}

func TestCommentPublishingWaiters(t *testing.T) {
	for _, tc := range []struct{ name, first, second, table, a, b string }{
		{"table replacement", "COMMENT ON TABLE t IS 'first'", "COMMENT ON TABLE t IS 'second'", "second", "", ""},
		{"independent columns", "COMMENT ON COLUMN t.a IS 'first'", "COMMENT ON COLUMN t.b IS 'second'", "", "first", "second"},
		{"table and column", "COMMENT ON TABLE t IS 'first'", "COMMENT ON COLUMN t.a IS 'second'", "first", "second", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			first := sessionStart(t, e, nil)
			second := sessionStart(t, e, nil)
			sessionSQL(t, holder, "CREATE TABLE t(a INT,b INT)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			original := e.CatalogRead().state.descriptors[ref.Address].(*relationDescriptor)
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			firstDone := lockAsync(e, first, context.Background(), tc.first, false)
			lockPending(t, e, 1)
			secondDone := lockAsync(e, second, context.Background(), tc.second, false)
			lockPending(t, e, 2)
			transactionEnd(t, holder, tx, false)
			lockAwait(t, firstDone, nil)
			lockAwait(t, secondDone, nil)
			commentWant(t, e, e.CatalogRead(), ref, tc.table)
			ref.Address.SubID = 1
			commentWant(t, e, e.CatalogRead(), ref, tc.a)
			ref.Address.SubID = 2
			commentWant(t, e, e.CatalogRead(), ref, tc.b)
			current := e.CatalogRead().state.descriptors[original.address].(*relationDescriptor)
			if current.owner != original.owner || !reflect.DeepEqual(current.attributes, original.attributes) || current.address != original.address {
				t.Fatal("serialized comments changed object authority/column identities")
			}
			sessionSQL(t, first, "INSERT INTO t VALUES(7,8)")
			sessionSQL(t, second, "INSERT INTO t VALUES(9,10)")
			lockEmpty(t, e)
		})
	}
}

func TestCommentRestoreAuthorityIsolation(t *testing.T) {
	source := New()
	run(t, source, "CREATE USER ordinary")
	for _, tc := range []struct {
		name         string
		engine       *Engine
		actor, query string
	}{
		{"public operation", source, "postgres", "COMMENT ON SCHEMA public IS 'forbidden'"},
		{"non-superuser private authenticator", source.restoreStage(), "ordinary", "COMMENT ON SCHEMA public IS 'forbidden'"},
		{"shared role excluded", source.restoreStage(), "postgres", "COMMENT ON ROLE ordinary IS 'forbidden'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op, done := roleOperation(t, tc.engine, tc.actor)
			defer done()
			before := tc.engine.CatalogRead()
			rolesPublication := tc.engine.roles
			identity := op.RoleState()
			op.restoreCommentAuthority = true
			_, err := tc.engine.Execute(op, statement(t, tc.query), 0, nil)
			op.restoreCommentAuthority = false
			var diagnostic *sqlstate.Error
			if err == nil || errors.As(err, &diagnostic) {
				t.Fatal("invalid private marker accepted or fabricated SQL diagnostic", err)
			}
			if tc.engine.CatalogRead().state != before.state || tc.engine.roles != rolesPublication || op.RoleState() != identity {
				t.Fatal("invalid private marker changed authority/metadata")
			}
		})
	}
}
