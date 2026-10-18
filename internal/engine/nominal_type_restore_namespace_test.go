package engine

import (
	"bytes"
	"context"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestNominalTypeRestorePublicNamespaceAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	roleExecute(t, e, op, "CREATE ROLE namespace_domain_owner")
	withPublicSchemaUnitOwner(t, e, "namespace_domain_owner", func() {
		roleExecute(t, e, op, "SET ROLE namespace_domain_owner")
		roleExecute(t, e, op, "CREATE DOMAIN public.namespace_domain AS INT")
		roleExecute(t, e, op, "CREATE TYPE public.namespace_enum AS ENUM('ok')")
		roleExecute(t, e, op, "CREATE TYPE public.namespace_record AS (i INT)")
		roleExecute(t, e, op, "RESET ROLE")
	})
	roleExecute(t, e, op, "CREATE TABLE public.namespace_domain_rows(v public.namespace_domain,e public.namespace_enum,c public.namespace_record)")
	roleExecute(t, e, op, "INSERT INTO public.namespace_domain_rows VALUES(7,'ok','(8)')")
	roleExecute(t, e, op, "SET ROLE namespace_domain_owner")
	roleError(t, e, op, "CREATE DOMAIN public.ordinary_denied AS INT", "42501", "permission denied for schema public", "")
	roleError(t, e, op, "CREATE TYPE public.enum_denied AS ENUM('ok')", "42501", "permission denied for schema public", "")
	roleError(t, e, op, "CREATE TYPE public.record_denied AS (i INT)", "42501", "permission denied for schema public", "")
	roleExecute(t, e, op, "RESET ROLE")
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	done()
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("genuine named-owner domain stream", err)
	}
	read, finish := roleOperation(t, e, "postgres")
	defer finish()
	catalog := e.CatalogRead()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	address := catalog.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: public, Name: "namespace_domain"})
	domain, ok := catalog.Type(address)
	if !ok || domain.owner != e.Roles().LookupRef("namespace_domain_owner") || !domain.typ.IsDomain() {
		t.Fatal("restore changed domain owner or identity", domain)
	}
	for _, name := range []string{"namespace_enum", "namespace_record"} {
		address := catalog.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: public, Name: name})
		typ, ok := catalog.Type(address)
		if !ok || typ.owner != e.Roles().LookupRef("namespace_domain_owner") {
			t.Fatal("restore changed nominal type owner or identity", name, typ)
		}
	}
	result, err := e.Execute(read, statement(t, "SELECT v::int4,e::text,c::text FROM public.namespace_domain_rows"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := tuples(result)
	result.Rows.Close()
	if len(rows) != 1 || len(rows[0]) != 3 || rows[0][0] != int64(7) || rows[0][1] != "ok" || rows[0][2] != "(8)" {
		t.Fatal("trusted replay changed stored domain rows", rows)
	}
	roleExecute(t, e, read, "SET ROLE namespace_domain_owner")
	roleError(t, e, read, "CREATE DOMAIN public.still_denied AS INT", "42501", "permission denied for schema public", "")
	roleError(t, e, read, "CREATE TYPE public.enum_still_denied AS ENUM('ok')", "42501", "permission denied for schema public", "")
	roleError(t, e, read, "CREATE TYPE public.record_still_denied AS (i INT)", "42501", "permission denied for schema public", "")
	roleExecute(t, e, read, "RESET ROLE")
	var again bytes.Buffer
	if err := e.Dump(context.Background(), &again); err != nil || again.String() != dump.String() {
		t.Fatal("trusted replay changed complete stream", err, again.String(), dump.String())
	}
}

func TestNominalTypeRestoreDestinationFence(t *testing.T) {
	for _, tc := range []struct{ kind, query string }{
		{"domain", "CREATE DOMAIN public.forged_restore AS INT"},
		{"enum", "CREATE TYPE public.forged_restore AS ENUM('ok')"},
		{"composite", "CREATE TYPE public.forged_restore AS (i INT)"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			before := e.catalog.state
			op.restoreTableDestination = true
			defer func() { op.restoreTableDestination = false }()
			_, err := e.Execute(op, statement(t, tc.query), 0, nil)
			if err == nil || !strings.Contains(err.Error(), "invalid trusted restore "+tc.kind+" destination authority") || e.catalog.state != before {
				t.Fatal("destination flag bypassed isolated importer fence", err)
			}
		})
		t.Run(tc.kind+"_ordinary_authenticator", func(t *testing.T) {
			e := New()
			admin, closeAdmin := roleOperation(t, e, "postgres")
			roleExecute(t, e, admin, "CREATE ROLE nominal_untrusted LOGIN")
			closeAdmin()
			op, done := roleOperation(t, e, "nominal_untrusted")
			defer done()
			before := e.catalog.state
			e.importTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef)
			op.restoreTableDestination = true
			defer func() { op.restoreTableDestination = false; e.importTargets = nil }()
			_, err := e.Execute(op, statement(t, tc.query), 0, nil)
			if err == nil || !strings.Contains(err.Error(), "invalid trusted restore "+tc.kind+" destination authority") || e.catalog.state != before {
				t.Fatal("ordinary authenticator bypassed importer fence", err)
			}
		})
	}
}
