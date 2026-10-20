package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestEnumCatalogStagingAndAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	stage := domainUnitStage(t, e, op, "CREATE TYPE public.mood AS ENUM ('sad','ok','happy')")
	if e.CatalogRead().State() != before.State() {
		t.Fatal("stage published")
	}
	domainUnitPublish(t, e, stage)
	mood := domainUnitType(t, e.CatalogRead(), "mood")
	if !mood.SQLType.IsEnum() || mood.Owner != op.RoleState().CurrentUser || !typedef.DefaultDomainACL(mood) || !reflect.DeepEqual(mood.SQLType.EnumLabels(), []string{"sad", "ok", "happy"}) {
		t.Fatal(mood)
	}
	for _, labels := range []string{"'ok','ok'", "'" + strings.Repeat("x", 64) + "'"} {
		raw := statement(t, "CREATE TYPE public.failed AS ENUM ("+labels+")")
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err == nil {
			err = e.stageCreateEnumLocked(op, auth, e.BeginCatalogStage(auth.Catalog), raw.(*parser.CreateEnum))
		}
		e.mu.Unlock()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || (diagnostic.Code != "23505" && diagnostic.Code != "42602") {
			t.Fatal(err)
		}
		if len(e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: mood.Namespace(), Name: "failed"})) != 0 {
			t.Fatal("failure leaked type")
		}
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE public.failed AS ENUM ()"))
	canceled := domainUnitStage(t, e, op, "CREATE TYPE public.canceled AS ENUM ('x')")
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, canceled)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	previous := e.CatalogRead().State()
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: canceled.Base.Epoch()}); !errors.Is(err, context.Canceled) || e.CatalogRead().State() != previous {
		t.Fatal("canceled publication", err)
	}
	e.mu.Lock()
	reader := e.roles.domain.Principal(e.roles.nextPrincipal)
	e.roles.nextPrincipal++
	roles := cloneRoleFixture(e.roles.published)

	putFixtureRole(roles, authorization.RoleDescriptor{Ref: reader, Name: "reader"})
	e.roles.published = roles
	e.mu.Unlock()
	auth := catalogAuthorization(e, e.CatalogRead())
	auth.State.CurrentUser = reader
	// Grant schema access through the authority fixture, without adding SQL GRANT.
	ns := *auth.Catalog.MustNamespace(mood.Namespace())
	ns.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: ns.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}
	namespaceStage := e.BeginCatalogStage(auth.Catalog)
	ns.ObjectRevision++
	if err := namespaceStage.Replace(&ns); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, namespaceStage)
	auth = catalogAuthorization(e, e.CatalogRead())
	auth.State.CurrentUser = reader
	dependent := statement(t, "CREATE DOMAIN public.consumer AS public.mood").(*parser.CreateDomain)
	e.mu.Lock()
	consumer := e.BeginCatalogStage(auth.Catalog)
	err = e.stageCreateDomainLocked(op, auth, consumer, dependent, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.mu.Unlock()
	if err != nil {
		t.Fatal("PUBLIC USAGE did not admit distinct consumer", err)
	}
	if len(descriptorReferences(mood)) == 0 {
		t.Fatal("enum owner and ACL references missing")
	}
	next := *mood
	next.ACL = authorization.ACLValue{Explicit: true}
	next.ACLSyntheticDefault = false
	next.ObjectRevision++
	revoke := e.BeginCatalogStage(e.CatalogRead())
	if err := revoke.Replace(&next); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, revoke)
	auth = catalogAuthorization(e, e.CatalogRead())
	auth.State.CurrentUser = reader
	e.mu.Lock()
	denied := e.BeginCatalogStage(auth.Catalog)
	err = e.stageCreateDomainLocked(op, auth, denied, dependent, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.mu.Unlock()
	domainUnitDiagnostic(t, err, "42501", "permission denied for type mood")
	if denied.DescriptorCount() != 0 {
		t.Fatal("unauthorized dependent staged")
	}
}

func TestEnumExplicitCatalogRemapping(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE public.mood AS ENUM ('sad','ok','happy')"))
	mood := domainUnitType(t, e.CatalogRead(), "mood")
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.wrapped AS public.mood"))
	wrapped := domainUnitType(t, e.CatalogRead(), "wrapped")
	next := catalog.CloneState(e.CatalogRead().State())
	target := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 60000}
	changed, err := typedef.ReownDomainTypes(next, map[ids.ObjectAddress]ids.ObjectAddress{mood.Address(): target}, false)
	if err != nil || !changed {
		t.Fatal(err)
	}
	newMood := next.MustType(mood.Address()).SQLType
	newWrapped := next.MustType(wrapped.Address()).SQLType
	if newMood.ID() != sqltype.ID(target.ObjectID) || newWrapped.BaseType() != newMood || mood.SQLType.ID() == newMood.ID() {
		t.Fatal("identity mapping lost")
	}
	for _, typ := range []*sqltype.Descriptor{mood.SQLType, wrapped.SQLType} {
		value, err := typ.Input(context.Background(), "happy", -1)
		if err != nil {
			t.Fatal(err)
		}
		destination := newMood
		if typ == wrapped.SQLType {
			destination = newWrapped
		}
		if _, err := typedef.ReownDomainDatum(value, destination, nil, nil, nil); err == nil {
			t.Fatal("cross-ID enum import admitted without source authority")
		}
		foreign, err := sqltype.EnumWithMembers(newMood.ID(), newMood.Name(), newMood.SQLName(), newMood.EnumMembers())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := typedef.ReownDomainDatum(value, destination, nil, map[*sqltype.Descriptor]*sqltype.Descriptor{mood.SQLType: foreign}, nil); err == nil {
			t.Fatal("equal-ID/member/label destination bypassed exact codec authority")
		}
		mapped, err := typedef.ReownDomainDatum(value, destination, nil, map[*sqltype.Descriptor]*sqltype.Descriptor{mood.SQLType: newMood}, nil)
		if err != nil {
			t.Fatal(err)
		}
		output, err := destination.Output(context.Background(), mapped)
		if err != nil || output != "happy" {
			t.Fatal(output, err)
		}
	}
	if mood.SQLType.ID() != sqltype.ID(mood.Address().ObjectID) {
		t.Fatal("source mutated")
	}
}

func TestEnumNoFromAdmission(t *testing.T) {
	e := New()
	run(t, e, "CREATE TYPE mood AS ENUM ('sad','ok','happy')")
	run(t, e, "CREATE TYPE happiness AS ENUM ('happy','ok','sad')")
	run(t, e, "CREATE DOMAIN wrapped AS mood")
	for _, tc := range []struct {
		query string
		args  []driver.NamedValue
		want  [][]driver.Value
	}{
		{"SELECT 'ok'::mood", nil, [][]driver.Value{{"ok"}}},
		{"SELECT $1::mood", []driver.NamedValue{{Ordinal: 1, Value: "sad"}}, [][]driver.Value{{"sad"}}},
		{"SELECT NULL::mood = 'ok'::mood", nil, [][]driver.Value{{nil}}},
		{"SELECT 'sad'::mood < 'happy'::mood", nil, [][]driver.Value{{true}}},
		{"SELECT 'ok'::mood::text = 'ok'::happiness::text", nil, [][]driver.Value{{true}}},
		{"SELECT NULL::mood IS NULL", nil, [][]driver.Value{{true}}},
		{"SELECT 'ok'::mood, 'sad'::mood", nil, [][]driver.Value{{"ok", "sad"}}},
	} {
		ast := statement(t, tc.query)
		if _, err := testDescribe(e, ast); err != nil {
			t.Fatal(tc.query, err)
		}
		result, err := testExecute(e, context.Background(), ast, tc.args)
		if err != nil {
			t.Fatal(tc.query, err)
		}
		if got := tuples(result); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.query, got, tc.want)
		}
	}
	for _, query := range []string{
		"SELECT 1::integer",
		"SELECT 'ok'::text",
		"SELECT 'ok'::wrapped",
		"SELECT 'ok'::mood, 1::integer",
	} {
		if _, err := testDescribe(e, statement(t, query)); err != nil {
			t.Fatal("ordinary no-FROM cast rejected", query, err)
		}
	}
	for _, tc := range []struct {
		query, code, message, position string
	}{
		{"SELECT 'nope'::mood", "22P02", `invalid input value for enum mood: "nope"`, "8"},
		{"SELECT NULL::replacement", "42704", `type "replacement" does not exist`, "14"},
		{"SELECT 'ok'::mood(1)", "42601", `type modifier is not allowed for type "mood"`, "14"},
	} {
		_, err := testDescribe(e, statement(t, tc.query))
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal(tc.query, diagnostic, err, want)
		}
	}
}
