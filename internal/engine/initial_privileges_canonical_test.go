package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
)

func TestPGInitPrivsNativeCanonicalDump(t *testing.T) {
	ctx := context.Background()
	e := New()
	var pristine bytes.Buffer
	if err := Dump(ctx, e, &pristine); err != nil || pristine.Len() != 0 {
		t.Fatal("canonical pristine dump changed", pristine.String(), err)
	}
	run(t, e, `CREATE TABLE canonical_initial_acl (a integer)`)
	run(t, e, `GRANT SELECT ON canonical_initial_acl TO PUBLIC`)
	var ordinary bytes.Buffer
	if err := Dump(ctx, e, &ordinary); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ordinary.String(), control.InitialPrivilegesMarker) {
		t.Fatal("ordinary creation/current ACL added historical metadata")
	}
	copy := New()
	if err := Restore(ctx, copy, strings.NewReader(ordinary.String())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := Dump(ctx, copy, &again); err != nil || ordinary.String() != again.String() {
		t.Fatal("ordinary canonical dump did not round trip", err)
	}

	// Absence alone, without any extension record or ordinary object, must
	// still emit a complete replacement inventory rather than inherit seed i.
	absent := New()
	run(t, absent, `DROP SCHEMA public CASCADE`)
	run(t, absent, `CREATE SCHEMA public`)
	var removed bytes.Buffer
	if err := Dump(ctx, absent, &removed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(removed.String(), control.InitialPrivilegesMarker) {
		t.Fatal("bootstrap absence omitted metadata")
	}
	restored := New()
	if err := Restore(ctx, restored, strings.NewReader(removed.String())); err != nil {
		t.Fatal(err)
	}
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if restored.Catalog.State.InitialPrivilege(public) != nil {
		t.Fatal("absent bootstrap history regenerated")
	}
	var roundTrip bytes.Buffer
	if err := Dump(ctx, restored, &roundTrip); err != nil || removed.String() != roundTrip.String() {
		t.Fatal("bootstrap absence metadata did not round trip", err)
	}
}
