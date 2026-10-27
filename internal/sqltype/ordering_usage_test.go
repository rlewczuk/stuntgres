package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestOrderingUsage(t *testing.T) {
	for _, d := range Builtins() {
		t.Run(d.Name(), func(t *testing.T) {
			provider, err := BindOrdering(d.Usage())
			if !d.Concrete() {
				if err == nil {
					t.Fatal("lookup-only identity admitted to ordering")
				}
				return
			}
			// Concrete means a valid runtime result, not a sortable SQL type.
			if d.ID() == 2275 {
				if err == nil {
					t.Fatal("cstring carrier granted ordering")
				}
				return
			}
			if d.ID() == PgSnapshot {
				if err == nil {
					t.Fatal("pg_snapshot ordering admitted")
				}
				return
			}
			switch d.ID() {
			case PgNDistinct, PgDependencies, PgMCVList:
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an ordering operator for type " + d.Name(), Hint: "Use an explicit ordering operator or modify the query."}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || *diagnostic != want || provider != (Comparison{}) {
					t.Fatalf("statistics ordering got %#v (%v), want %#v", diagnostic, err, want)
				}
				return
			}
			if d.ID() == Refcursor || d.ID() == Box || d.ID() == Point || d.ID() == Lseg || d.ID() == Path || d.ID() == Circle || d.ID() == Line || d.ID() == Polygon || d.ID() == Macaddr8 || IsFTS(d.ID()) || d.ID() == ACLItem || d.ID() == Void || d.IsArray() && !d.IsCatalogVector() || d.ID() == JSON || d.ID() == JSONPath || isBitString(d.ID()) || d.ID() == Xid || d.ID() == Cid || d.IsRange() || d.IsMultirange() {
				if err == nil {
					t.Fatalf("%s ordering admitted", d.Name())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// SQL null placement is the sort stage's policy, never this strict
			// nonnull provider, even when the builtin capability binds successfully.
			if _, err := provider.Compare(context.Background(), Null(d), Null(d), InputEnvironment{}); err == nil {
				t.Fatal("nonnull provider admitted NULL")
			}
			invalid := d.Usage()
			invalid.Collation.ID = 999999
			if _, err := BindOrdering(invalid); err == nil {
				t.Fatal("unbounded collation admitted")
			}
		})
	}
	if _, err := BindOrdering(Usage{}); err == nil {
		t.Fatal("nil ordering usage admitted")
	}
}
