package enginetests_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestBackendIdentityProjection(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE ROLE bi_unit_login", "CREATE ROLE bi_unit_owner", "CREATE ROLE bi_unit_middle", "CREATE ROLE bi_unit_transitive", "CREATE ROLE bi_unit_stats", "CREATE ROLE bi_unit_other", "CREATE ROLE bi_unit_noinherit", "CREATE ROLE bi_unit_setonly", "CREATE ROLE bi_unit_adminonly",
		"GRANT bi_unit_login TO bi_unit_owner", "GRANT bi_unit_login TO bi_unit_middle", "GRANT bi_unit_middle TO bi_unit_transitive", "GRANT pg_read_all_stats TO bi_unit_stats",
		"GRANT bi_unit_login TO bi_unit_noinherit WITH INHERIT FALSE", "GRANT bi_unit_login TO bi_unit_setonly WITH INHERIT FALSE, SET TRUE", "GRANT bi_unit_login TO bi_unit_adminonly WITH ADMIN TRUE, INHERIT FALSE, SET FALSE",
	} {
		enginetest.Run(t, e, q)
	}
	view := e.CatalogRead()
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	login, _ := roles.Lookup("bi_unit_login")
	at := time.Unix(100, 0).UTC()
	a := statistics.Activity{BackendID: 0, Incarnation: 1, Principal: login.Ref, Database: 123, BackendStart: at}
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: roles, Activities: map[ids.SessionToken]statistics.Activity{s.Token(): a}}
	kinds := []functions.BackendIdentityKind{functions.BackendPID, functions.BackendDatabaseID, functions.BackendUserID, functions.BackendStart, functions.BackendClientAddress, functions.BackendClientPort}
	for _, name := range []string{"postgres", "bi_unit_login", "bi_unit_owner", "bi_unit_transitive", "bi_unit_stats", "bi_unit_other", "bi_unit_noinherit", "bi_unit_setonly", "bi_unit_adminonly"} {
		observer, _ := roles.Lookup(name)
		in.ActivityObserver = observer.Ref
		allowed := name == "postgres" || name == "bi_unit_login" || name == "bi_unit_owner" || name == "bi_unit_transitive" || name == "bi_unit_stats"
		idsType, _ := view.LookupType(sqltype.Int4)
		values, err := pgcatalog.BackendIdentityIDs(in, idsType)
		if err != nil || len(values) != 1 {
			t.Fatal("public ID enumeration", name, values, err)
		}
		if n, err := values[0].Integer(); err != nil || n != 0 {
			t.Fatal("zero is a represented slot", n, err)
		}
		for _, kind := range kinds {
			typ, _ := view.LookupType(kind.ResultType())
			v, err := pgcatalog.BackendIdentityValue(in, kind, 0, typ)
			if err != nil || v.Type() != typ {
				t.Fatal("typed projection", name, kind, v, err)
			}
			switch kind {
			case functions.BackendPID:
				if n, err := v.Integer(); err != nil || n != int64(s.Token().ID()) {
					t.Fatal("PID independent of zero slot", n, err)
				}
			case functions.BackendDatabaseID:
				if n, err := v.OID(); err != nil || n != 123 {
					t.Fatal("public database OID", n, err)
				}
			case functions.BackendUserID:
				if n, err := v.OID(); err != nil || n != uint32(login.Ref.ID()) {
					t.Fatal("public immutable login OID", n, err)
				}
			case functions.BackendStart:
				if v.IsNull() == allowed {
					t.Fatal("current protected start", name, v)
				}
				if allowed {
					if got, err := v.TimestampTime(); err != nil || !got.Equal(at) {
						t.Fatal(got, err)
					}
				}
			default:
				if !v.IsNull() {
					t.Fatal("invented in-process endpoint", v)
				}
			}
			for _, absent := range []int64{-1, -2147483648, 2147483647} {
				null, err := pgcatalog.BackendIdentityValue(in, kind, absent, typ)
				if err != nil || !null.IsNull() || null.Type() != typ {
					t.Fatal("typed absent boundary", kind, absent, null, err)
				}
			}
		}
	}
	startType, _ := view.LookupType(sqltype.Timestamptz)
	stats, _ := roles.Lookup("bi_unit_stats")
	in.ActivityObserver = stats.Ref
	zero := a
	zero.BackendStart = time.Time{}
	in.Activities[s.Token()] = zero
	if v, err := pgcatalog.BackendIdentityValue(in, functions.BackendStart, 0, startType); err != nil || !v.IsNull() {
		t.Fatal("absent birth timestamp", v, err)
	}
	in.Activities[s.Token()] = a
	// A retained target may disappear from the current role root. Public OID
	// and all-stats protected facts survive, ordinary owner authority does not.
	enginetest.Run(t, e, "DROP ROLE bi_unit_login")
	in.Catalog = e.CatalogRead()
	in.Roles = e.RoleStore.PublishedRead(&e.Catalog.Owner, in.Catalog)
	for _, name := range []string{"postgres", "bi_unit_stats", "bi_unit_owner", "bi_unit_other"} {
		observer, _ := in.Roles.Lookup(name)
		in.ActivityObserver = observer.Ref
		v, err := pgcatalog.BackendIdentityValue(in, functions.BackendStart, 0, startType)
		allowed := name == "postgres" || name == "bi_unit_stats"
		if err != nil || v.IsNull() == allowed {
			t.Fatal("dropped retained principal policy", name, v, err)
		}
		userType, _ := view.LookupType(sqltype.Oid)
		v, err = pgcatalog.BackendIdentityValue(in, functions.BackendUserID, 0, userType)
		if n, failure := v.OID(); err != nil || failure != nil || n != uint32(login.Ref.ID()) {
			t.Fatal("retained public dropped OID", n, err, failure)
		}
	}
	enginetest.Run(t, e, "CREATE ROLE bi_unit_login")
	in.Catalog = e.CatalogRead()
	in.Roles = e.RoleStore.PublishedRead(&e.Catalog.Owner, in.Catalog)
	recreated, _ := in.Roles.Lookup("bi_unit_login")
	if recreated.Ref == login.Ref {
		t.Fatal("role recreation reused identity")
	}
	in.ActivityObserver = recreated.Ref
	if v, err := pgcatalog.BackendIdentityValue(in, functions.BackendStart, 0, startType); err != nil || !v.IsNull() {
		t.Fatal("recreated spelling adopted retained login", v, err)
	}
	for _, invalid := range []authorization.PrincipalRef{{}, new(authorization.Domain).Principal(42)} {
		bad := a
		bad.Principal = invalid
		in.Activities[s.Token()] = bad
		if _, err := pgcatalog.BackendIdentityValue(in, functions.BackendPID, 0, enginetest.Usage(e, sqltype.Int4).Type); err == nil {
			t.Fatal("invalid principal provenance accepted")
		}
		if _, err := pgcatalog.BackendIdentityIDs(in, enginetest.Usage(e, sqltype.Int4).Type); err == nil {
			t.Fatal("invalid ID-set provenance accepted")
		}
	}
	in.Activities[s.Token()] = a
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context = ctx
	if values, err := pgcatalog.BackendIdentityIDs(in, enginetest.Usage(e, sqltype.Int4).Type); !errors.Is(err, context.Canceled) || values != nil {
		t.Fatal("canceled ID enumeration published prefix", values, err)
	}
	if v, err := pgcatalog.BackendIdentityValue(in, functions.BackendStart, 0, startType); !errors.Is(err, context.Canceled) || v.Type() != nil {
		t.Fatal("canceled scalar published value", v, err)
	}
}
