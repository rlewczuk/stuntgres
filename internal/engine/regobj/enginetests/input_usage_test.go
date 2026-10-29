package regobj_test

import (
	"context"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestInputValidityResolverWarnings(t *testing.T) {
	_, view, path := typeDescribeCatalog()
	op := &session.Operation{NoticeThreshold: session.LevelWarning}
	input := regobj.NewTypeInput(regobj.InputCapture{Authority: regobj.Authority{View: access.View{Catalog: view}}, Path: path}, op)
	usage, err := input.ResolveInputUsage(context.Background(), "timestamp(7)")
	if err != nil || usage.Type.ID() != sqltype.Timestamp || usage.Typmod != 6 {
		t.Fatal("warning lost usage", usage, err)
	}
	notices := op.DetachNotices()
	if len(notices) != 1 || notices[0].Code != "22023" || notices[0].Message != "TIMESTAMP(7) precision reduced to maximum allowed, 6" || notices[0].Severity != "WARNING" {
		t.Fatal("modifier warning", notices)
	}
	// OID-facing declarations share validation but never expose the modifier as
	// an OID or reinterpret REGTYPE's own value shortcuts.
	raw, err := parser.ParseTypeName(context.Background(), "numeric(7,4)", nil)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := input.ResolveParsedTypeName(context.Background(), raw, op)
	if err != nil || oid != uint32(sqltype.Numeric) {
		t.Fatal("parsed OID contract", oid, err)
	}
	usage, err = input.ResolveInputUsage(context.Background(), "numeric(7,4)[]")
	if err != nil || usage.Typmod != 458760 || !usage.Type.IsArray() || usage.Type.Element().ID() != sqltype.Numeric {
		t.Fatal("array element typmod", usage, err)
	}
}
