package engine

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestPGSettingsCatalogACLRecheck(t *testing.T) {
	e := New()
	d := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.SettingsOID})
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	role := roleAttributeCreate(t, e, admin, "settings_metadata_reader", true, parser.RoleOptions{})
	reader, release := roleOperation(t, e, role.Name)
	defer release()
	retained, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	requirement := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}
	if err := retained.Check(requirement); err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(t.Context(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	denied, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table pg_settings")
	for _, columns := range [][]int{nil, {0}, {0, 1}} {
		domainUnitDiagnostic(t, denied.CheckColumns(ref, e.CatalogRead().MustRelation(d.Address()), authorization.SelectPrivilege, columns), "42501", "permission denied for table pg_settings")
	}
	if err := retained.Check(requirement); err != nil {
		t.Fatal("retained authority changed", err)
	}
	column := ref
	column.Address.SubID = d.Attributes[0]
	if err := e.SetObjectACL(t.Context(), column, d.Security.ACL); err != nil {
		t.Fatal(err)
	}
	columnReader, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead().MustRelation(d.Address())
	if err := columnReader.CheckColumns(ref, current, authorization.SelectPrivilege, []int{0}); err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, columnReader.CheckColumns(ref, current, authorization.SelectPrivilege, []int{1}), "42501", "permission denied for table pg_settings")
	if err := e.SetObjectACL(t.Context(), column, authorization.ACLValue{}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(t.Context(), ref, d.Security.ACL); err != nil {
		t.Fatal(err)
	}
	restored, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Check(requirement); err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table pg_settings")
}

// These are engine/package seams, not public driver SQL witnesses. In particular,
// a held description is an identity capture, never a cached SELECT permission.
func TestPGSettingsCatalogACLConsumers(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	role := roleAttributeCreate(t, e, admin, "settings_consumer_reader", true, parser.RoleOptions{})
	reader, release := roleOperation(t, e, role.Name)
	defer release()
	roleExecute(t, e, reader, "SET application_name='acl sentinel'")
	d := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.SettingsOID})
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	column := ref
	column.Address.SubID = d.Attributes[0]
	setACL := func(t *testing.T, target ids.ObjectRef, acl authorization.ACLValue) {
		t.Helper()
		if err := e.SetObjectACL(t.Context(), target, acl); err != nil {
			t.Fatal(err)
		}
	}
	deny := func(t *testing.T, err error) {
		t.Helper()
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table pg_settings"}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatalf("denial: %#v (%v), want %#v", diagnostic, err, want)
		}
	}
	check := func(t *testing.T, result Result, err error, denied bool, want [][]driver.Value) {
		t.Helper()
		if denied {
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("denied consumer published rows")
			}
			if result.Count != 0 {
				t.Fatal("denied consumer published count", result.Count)
			}
			deny(t, err)
			return
		}
		if err != nil || result.Rows == nil {
			t.Fatal("healthy consumer", result, err)
		}
		defer result.Rows.Close()
		got := tuples(result)
		if len(got) != len(want) || (len(want) != 0 && !reflect.DeepEqual(got, want)) {
			t.Fatalf("rows %#v, want %#v", got, want)
		}
	}
	type consumer struct {
		query    string
		nameOnly bool
		want     [][]driver.Value
		ast      parser.Statement
		held     StatementDescription
	}
	consumers := []consumer{
		{query: "SELECT name FROM pg_catalog.pg_settings WHERE name='application_name'", nameOnly: true, want: [][]driver.Value{{"application_name"}}},
		{query: "SELECT name FROM pg_catalog.pg_settings WHERE false", nameOnly: true},
		{query: "SELECT setting FROM pg_catalog.pg_settings WHERE name='application_name'", want: [][]driver.Value{{"acl sentinel"}}},
		{query: "SELECT setting FROM pg_catalog.pg_settings WHERE false"},
		{query: "SELECT name FROM pg_catalog.pg_settings WHERE setting='acl sentinel'", want: [][]driver.Value{{"application_name"}}},
		{query: "SELECT name FROM pg_catalog.pg_settings WHERE setting='acl sentinel' AND false"},
		{query: "SELECT * FROM pg_catalog.pg_settings WHERE false"},
	}
	for i := range consumers {
		c := &consumers[i]
		c.ast = statement(t, c.query)
		var err error
		c.held, err = e.Describe(reader, c.ast)
		if err != nil {
			t.Fatal(c.query, err)
		}
	}
	execute := func(t *testing.T, c *consumer, expected *StatementDescription) (Result, error) {
		t.Helper()
		scope, err := e.StartStatement(reader)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = expected
		result, err := scope.Execute(c.ast, 0, nil)
		return result, scope.Finish(err)
	}
	healthySettings := func(t *testing.T) {
		t.Helper()
		result, err := e.Execute(reader, statement(t, "SHOW application_name"), 0, nil)
		check(t, result, err, false, [][]driver.Value{{"acl sentinel"}})
		// The owner can still see the original boot/reset metadata during denial.
		result, err = e.Execute(admin, statement(t, "SELECT setting,boot_val,reset_val,source FROM pg_catalog.pg_settings WHERE name='application_name'"), 0, nil)
		check(t, result, err, false, [][]driver.Value{{"", "", "", "default"}})
	}
	for _, phase := range []string{"public", "denied", "name-only", "restored"} {
		switch phase {
		case "denied":
			setACL(t, ref, authorization.ACLValue{Explicit: true})
		case "name-only":
			setACL(t, column, d.Security.ACL)
		case "restored":
			setACL(t, column, authorization.ACLValue{})
			setACL(t, ref, d.Security.ACL)
		}
		for i := range consumers {
			c := &consumers[i]
			for _, route := range []string{"direct", "new-prepare", "held"} {
				t.Run(phase+"/"+route+"/"+c.query, func(t *testing.T) {
					denied := phase == "denied" || (phase == "name-only" && !c.nameOnly)
					var expected *StatementDescription
					switch route {
					case "new-prepare":
						description, err := e.Describe(reader, c.ast)
						if err != nil {
							t.Fatal("preparation must not execute the consumer", err)
						}
						expected = &description
					case "held":
						expected = &c.held
					}
					result, err := execute(t, c, expected)
					check(t, result, err, denied, c.want)
				})
			}
		}
		healthySettings(t)
	}
	// ACL publication after this statement's metadata admission invalidates its
	// captured publication environment. Preserve that replacement fence, then
	// prove that a fresh attempt under the revoked authority gets exact42501.
	for i := range consumers {
		c := &consumers[i]
		t.Run("source-admission/"+c.query, func(t *testing.T) {
			scope, err := e.StartStatement(reader)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Describe(c.ast)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			setACL(t, ref, authorization.ACLValue{Explicit: true})
			result, err := scope.Execute(c.ast, 0, nil)
			err = scope.Finish(err)
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("invalidated admission published rows")
			}
			if !errors.Is(err, ids.ErrCatalogReplaced) || result.Count != 0 {
				t.Fatal("invalidated admission", result, err)
			}
			result, err = execute(t, c, &c.held)
			check(t, result, err, true, nil)
			setACL(t, ref, d.Security.ACL)
			result, err = execute(t, c, &c.held)
			check(t, result, err, false, c.want)
			healthySettings(t)
		})
	}
	result, err := e.Execute(reader, statement(t, "SELECT setting,boot_val,reset_val,source FROM pg_catalog.pg_settings WHERE name='application_name'"), 0, nil)
	check(t, result, err, false, [][]driver.Value{{"acl sentinel", "", "", "session"}})
}
