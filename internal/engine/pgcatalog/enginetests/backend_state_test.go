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

func TestBackendStateProjection(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE ROLE bs_unit_login", "CREATE ROLE bs_unit_owner", "CREATE ROLE bs_unit_middle", "CREATE ROLE bs_unit_transitive", "CREATE ROLE bs_unit_stats", "CREATE ROLE bs_unit_other", "CREATE ROLE bs_unit_noinherit", "CREATE ROLE bs_unit_setonly", "CREATE ROLE bs_unit_adminonly",
		"GRANT bs_unit_login TO bs_unit_owner", "GRANT bs_unit_login TO bs_unit_middle", "GRANT bs_unit_middle TO bs_unit_transitive", "GRANT pg_read_all_stats TO bs_unit_stats",
		"GRANT bs_unit_login TO bs_unit_noinherit WITH INHERIT FALSE", "GRANT bs_unit_login TO bs_unit_setonly WITH INHERIT FALSE, SET TRUE", "GRANT bs_unit_login TO bs_unit_adminonly WITH ADMIN TRUE, INHERIT FALSE, SET FALSE",
	} {
		enginetest.Run(t, e, q)
	}
	view := e.CatalogRead()
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	login, _ := roles.Lookup("bs_unit_login")
	at := time.Unix(100, 123000).UTC()
	intType, _ := view.LookupType(sqltype.Int4)
	boolType, _ := view.LookupType(sqltype.Bool)
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "subxact_count", Usage: intType.Usage()}, {Attribute: 2, Name: "subxact_overflowed", Usage: boolType.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	a := statistics.Activity{BackendID: 0, Incarnation: 1, Principal: login.Ref, Query: "/* retained */ SELECT 7", QueryAvailable: true, Start: at, TransactionStart: at.Add(-time.Second), Wait: "relation", WaitType: "Lock", SubxactCount: 64, SubxactOverflowed: true}
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: roles, Activities: map[ids.SessionToken]statistics.Activity{s.Token(): a}}
	for _, name := range []string{"postgres", "bs_unit_login", "bs_unit_owner", "bs_unit_transitive", "bs_unit_stats", "bs_unit_other", "bs_unit_noinherit", "bs_unit_setonly", "bs_unit_adminonly"} {
		observer, _ := roles.Lookup(name)
		in.ActivityObserver = observer.Ref
		allowed := name == "postgres" || name == "bs_unit_login" || name == "bs_unit_owner" || name == "bs_unit_transitive" || name == "bs_unit_stats"
		for kind := functions.BackendStateActivity; kind <= functions.BackendStateSubxact; kind++ {
			typ, _ := view.LookupType(kind.ResultType())
			if kind == functions.BackendStateSubxact {
				typ = shape
			}
			v, err := pgcatalog.BackendStateValue(in, kind, 0, typ)
			if err != nil || v.Type() != typ {
				t.Fatal("typed projection", name, kind, v, err)
			}
			if kind == functions.BackendStateSubxact {
				count, ce := v.CompositeField(0).Integer()
				overflow, oe := v.CompositeField(1).Boolean()
				if v.IsNull() || ce != nil || oe != nil || count != 64 || !overflow {
					t.Fatal("subxact privilege independence", name, v, ce, oe)
				}
			} else if kind == functions.BackendStateActivityStart || kind == functions.BackendStateXactStart {
				if v.IsNull() == allowed {
					t.Fatal("timestamp authority", name, v)
				}
				if allowed {
					want := at
					if kind == functions.BackendStateXactStart {
						want = at.Add(-time.Second)
					}
					got, err := v.TimestampTime()
					if err != nil || !got.Equal(want) {
						t.Fatal("real timestamp", got, err)
					}
				}
			} else {
				want := "<insufficient privilege>"
				if allowed {
					switch kind {
					case functions.BackendStateActivity:
						want = a.Query
					case functions.BackendStateWaitEvent:
						want = "relation"
					case functions.BackendStateWaitEventType:
						want = "Lock"
					}
				}
				got, err := v.Text()
				if err != nil || got != want {
					t.Fatal("exact legacy text", name, got, want, err)
				}
			}
			for _, absent := range []int64{-1, -2147483648, 2147483647} {
				v, err := pgcatalog.BackendStateValue(in, kind, absent, typ)
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case functions.BackendStateSubxact:
					if v.IsNull() || !v.CompositeField(0).IsNull() || !v.CompositeField(1).IsNull() {
						t.Fatal("missing is nonnull record", v)
					}
				case functions.BackendStateActivityStart, functions.BackendStateXactStart:
					if !v.IsNull() {
						t.Fatal("missing time", v)
					}
				default:
					got, err := v.Text()
					if err != nil || got != "<backend information not available>" {
						t.Fatal("missing sentinel", v, err)
					}
				}
			}
		}
	}
	admin, _ := roles.Lookup("postgres")
	in.ActivityObserver = admin.Ref
	zero := a
	zero.Query = ""
	zero.QueryAvailable = false
	zero.Start = time.Time{}
	zero.TransactionStart = time.Time{}
	zero.Wait = ""
	zero.WaitType = ""
	zero.Disabled = true
	zero.SubxactCount = 0
	zero.SubxactOverflowed = false
	in.Activities[s.Token()] = zero
	for kind := functions.BackendStateActivity; kind <= functions.BackendStateSubxact; kind++ {
		typ, _ := view.LookupType(kind.ResultType())
		if kind == functions.BackendStateSubxact {
			typ = shape
		}
		v, err := pgcatalog.BackendStateValue(in, kind, 0, typ)
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case functions.BackendStateActivity:
			got, _ := v.Text()
			if got != "<command string not enabled>" {
				t.Fatal("disabled command sentinel", v)
			}
		case functions.BackendStateSubxact:
			count, _ := v.CompositeField(0).Integer()
			overflow, _ := v.CompositeField(1).Boolean()
			if count != 0 || overflow || v.IsNull() {
				t.Fatal("live zero cache", v)
			}
		default:
			if !v.IsNull() {
				t.Fatal("zero time/no wait", v)
			}
		}
	}
	for _, invalid := range []authorization.PrincipalRef{{}, new(authorization.Domain).Principal(42)} {
		bad := a
		bad.Principal = invalid
		in.Activities[s.Token()] = bad
		if v, err := pgcatalog.BackendStateValue(in, functions.BackendStateSubxact, 0, shape); err == nil || v.Type() != nil {
			t.Fatal("invalid provenance published record", v, err)
		}
	}
	in.Activities[s.Token()] = a
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context = ctx
	if v, err := pgcatalog.BackendStateValue(in, functions.BackendStateSubxact, 0, shape); !errors.Is(err, context.Canceled) || v.Type() != nil {
		t.Fatal("canceled projection published datum", v, err)
	}
	if _, err := pgcatalog.BackendStateValue(in, functions.BackendStateSubxact, 0, intType); err == nil {
		t.Fatal("invalid result shape admitted")
	}
}
