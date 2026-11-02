package enginetests

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestIntbigGkeyType(t *testing.T) {
	makeType := func(id sqltype.ID) *sqltype.Descriptor {
		d, err := sqltype.IntbigGkey(id, "intbig_gkey", "public.intbig_gkey")
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	d, same, destination := makeType(65000), makeType(65000), makeType(65001)
	if !d.Concrete() || !d.IsIntbigGkey() || !d.IsUserDefined() || d.IsBuiltin() || d.DatabaseName() != "INTBIG_GKEY" {
		t.Fatal("authentic support family identity")
	}
	metadata, ok := d.PGTypeMetadata()
	if !ok || metadata.Length != -1 || metadata.ByValue || metadata.Alignment != "i" || metadata.Storage != "p" || metadata.Kind != "b" || metadata.Category != "U" || metadata.Modifier != -1 || metadata.Collation != 0 {
		t.Fatal("released support metadata", metadata)
	}
	for _, tc := range []struct {
		id                sqltype.ID
		name, declaration string
	}{
		{0, "key", "public.key"}, {sqltype.Unknown, "key", "public.key"}, {65000, "", "public.key"}, {65000, "key", ""},
	} {
		if _, err := sqltype.IntbigGkey(tc.id, tc.name, tc.declaration); err == nil {
			t.Fatal("invalid support descriptor accepted", tc)
		}
	}
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot accept a value of type intbig_gkey"}
	for _, input := range []string{"", "x", "{}", "NULL", "é", "\x00"} {
		value, err := d.Input(t.Context(), input, -1)
		var diagnostic *sqlstate.Error
		if value.Type() != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatalf("rejecting key input %q value=%v diagnostic=%#v error=%v", input, value, diagnostic, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.Input(ctx, "x", -1); !errors.Is(err, context.Canceled) {
		t.Fatal("ordinary input cancellation", err)
	}
	null := sqltype.Null(d)
	if literal, err := d.SQLLiteral(t.Context(), null); err != nil || literal != "NULL" {
		t.Fatal("typed support NULL literal", literal, err)
	}
	if value, err := null.Retag(same); err != nil || value.Type() != same || !value.IsNull() {
		t.Fatal("NULL generation retag", value, err)
	}
	if _, err := null.Retag(destination); err == nil {
		t.Fatal("ordinary retag granted cross-OID import")
	}
	if value, err := null.RemapIntbigGkey(destination); err != nil || value.Type() != destination || !value.IsNull() || null.Type() != d {
		t.Fatal("authorized NULL import changed source", value, err)
	}
	wrong, err := sqltype.QueryInt(d.ID(), "intbig_gkey", "public.intbig_gkey")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := null.Retag(wrong); err == nil {
		t.Fatal("same OID/spelling granted foreign family authority")
	}
	if _, err := null.RemapIntbigGkey(wrong); err == nil {
		t.Fatal("foreign destination granted support import")
	}
	array, err := sqltype.Array(65002, "_intbig_gkey", d)
	if err != nil || array.Element() != d || !array.Concrete() {
		t.Fatal("real generic support array", array, err)
	}
	for _, input := range []string{"{}", "{NULL}", "[0:1]={NULL,NULL}"} {
		value, err := array.Input(t.Context(), input, -1)
		if err != nil {
			t.Fatal("support array input", input, err)
		}
		if output, err := array.Output(t.Context(), value); err != nil || output != input {
			t.Fatal("support array output", output, err)
		}
	}
	if value, err := array.Input(t.Context(), "{x}", -1); err == nil || value.Type() != nil {
		t.Fatal("nonnull array element bypassed rejecting key input", value, err)
	}
	if eq, order := sqltype.ComparisonCapabilities(d); eq || order {
		t.Fatal("support storage granted leaf comparison algorithms")
	}
	// There is deliberately no public nonnull key constructor. The output
	// branch's released rejection is implemented without fabricating a datum
	// solely for a test; a public key producer belongs to the index leaf.
}
