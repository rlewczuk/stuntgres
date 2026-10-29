package enginetests

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestHostHstoreOwnership(t *testing.T) {
	ctx := context.Background()
	typ, err := sqltype.Hstore(99001, "hstore", "public.hstore")
	if err != nil {
		t.Fatal(err)
	}
	entries := []sqltype.HstoreEntry{{Key: "a", Value: "1"}, {Key: "b", Null: true}}
	value, err := typ.HstoreFromEntries(ctx, entries)
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Value = "caller mutation"
	projection, err := value.OwnedHstoreEntries(ctx, typ)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection) != 2 || projection[0].Value != "1" || !projection[1].Null {
		t.Fatal("input carrier backing borrowed", projection)
	}
	projection[0].Value = "projection mutation"
	again, err := value.OwnedHstoreEntries(ctx, typ)
	if err != nil || again[0].Value != "1" {
		t.Fatal("private SQL backing escaped", again, err)
	}
	foreign, err := sqltype.Hstore(99001, "hstore", "public.hstore")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = value.OwnedHstoreEntries(ctx, foreign); err == nil {
		t.Fatal("coincident foreign generation admitted")
	}
	for _, bad := range [][]sqltype.HstoreEntry{{{Key: "bad\x00key", Value: "1"}}, {{Key: "a", Value: "bad\x00value"}}, {{Key: string([]byte{255}), Value: "1"}}, {{Key: "a", Null: true, Value: "contradiction"}}} {
		if _, err := typ.HstoreFromEntries(ctx, bad); err == nil {
			t.Fatal("invalid host carrier admitted")
		}
	}
	empty, err := typ.HstoreFromEntries(ctx, []sqltype.HstoreEntry{})
	if err != nil || empty.IsNull() {
		t.Fatal("empty map became SQL NULL", err)
	}
	projection, err = empty.OwnedHstoreEntries(ctx, typ)
	if err != nil || projection == nil || len(projection) != 0 {
		t.Fatal("empty nonnull projection lost", err)
	}
	if _, err := sqltype.Null(typ).OwnedHstoreEntries(ctx, typ); err == nil {
		t.Fatal("SQL NULL projected as nonnull map")
	}
	unicode, err := typ.HstoreFromEntries(ctx, []sqltype.HstoreEntry{{Key: "é", Value: "雪"}, {Key: "quote\"", Value: "slash\\"}})
	if err != nil {
		t.Fatal(err)
	}
	projection, err = unicode.OwnedHstoreEntries(ctx, typ)
	if err != nil || len(projection) != 2 {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := value.OwnedHstoreEntries(canceled, typ); !errors.Is(err, context.Canceled) {
		t.Fatal("projection ignored cancellation", err)
	}
	if _, err := typ.HstoreFromEntries(canceled, entries); !errors.Is(err, context.Canceled) {
		t.Fatal("construction ignored cancellation", err)
	}
}
