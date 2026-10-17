package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqltype/unicode16"
)

type versionInformationUnitContext struct{ ctx context.Context }

func (c versionInformationUnitContext) FunctionContext() context.Context { return c.ctx }
func (c versionInformationUnitContext) FunctionPropertiesMatch(p, rule IntrinsicPropertyRule) bool {
	return p == rule
}

func TestVersionInformationCarrier(t *testing.T) {
	var text, integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		switch typ.ID() {
		case sqltype.Text:
			text = typ
		case sqltype.Int4:
			integer = typ
		}
	}
	if text == nil || integer == nil {
		t.Fatal("missing builtin descriptors")
	}
	if unicode16.Version != "16.0.0" {
		t.Fatalf("provider contract changed: %q", unicode16.Version)
	}
	for _, tc := range []struct {
		kind VersionInformationKind
		want string
		null bool
	}{
		{VersionInformationServer, "PostgreSQL 18.0 compatible; embedded stuntgres", false},
		{VersionInformationUnicode, "16.0", false},
		{VersionInformationICU, "", true},
	} {
		t.Run(tc.kind.Name(), func(t *testing.T) {
			e := VersionInformation[versionInformationUnitContext, IntrinsicPropertyRule]{
				Info: Info[IntrinsicPropertyRule]{Usage: text.Usage(), Property: tc.kind.IntrinsicRule()}, Kind: tc.kind,
			}
			value, err := e.Eval(versionInformationUnitContext{context.Background()})
			if err != nil || value.Type() != text || value.IsNull() != tc.null {
				t.Fatalf("value=%#v err=%v", value, err)
			}
			if !tc.null {
				out, err := text.Output(context.Background(), value)
				if err != nil || out != tc.want {
					t.Fatalf("output=%q err=%v want=%q", out, err, tc.want)
				}
			}
			for _, change := range []func(*VersionInformation[versionInformationUnitContext, IntrinsicPropertyRule]){
				func(n *VersionInformation[versionInformationUnitContext, IntrinsicPropertyRule]) { n.Kind = 0 },
				func(n *VersionInformation[versionInformationUnitContext, IntrinsicPropertyRule]) {
					n.Usage = sqltype.Usage{}
				},
				func(n *VersionInformation[versionInformationUnitContext, IntrinsicPropertyRule]) {
					n.Usage = integer.Usage()
				},
				func(n *VersionInformation[versionInformationUnitContext, IntrinsicPropertyRule]) { n.Usage.Typmod = 7 },
				func(n *VersionInformation[versionInformationUnitContext, IntrinsicPropertyRule]) {
					n.Property = ExactVolatile
				},
			} {
				bad := e
				change(&bad)
				if _, err := bad.Eval(versionInformationUnitContext{context.Background()}); err == nil {
					t.Error("invalid carrier accepted")
				}
			}
			if _, err := e.Eval(versionInformationUnitContext{}); err == nil {
				t.Error("missing context accepted")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := e.Eval(versionInformationUnitContext{ctx}); !errors.Is(err, context.Canceled) {
				t.Errorf("precanceled Eval = %v", err)
			}
		})
	}
}
