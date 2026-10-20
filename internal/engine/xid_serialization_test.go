package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIdentifierLiteralSerializationStability(t *testing.T) {
	ctx := context.Background()
	e := New()
	for _, tc := range []struct{ input, want string }{
		{"'4294967295'::xid", "CAST('4294967295' AS XID)"},
		{"'4294967295'::cid", "CAST('4294967295' AS CID)"},
		{"'18446744073709551615'::xid8", "CAST('18446744073709551615' AS XID8)"},
		{"NULL::xid", "CAST(NULL AS XID)"},
		{"NULL::cid", "CAST(NULL AS CID)"},
		{"NULL::xid8", "CAST(NULL AS XID8)"},
		{"('4294967297'::xid8)::xid", "CAST(CAST('4294967297' AS XID8) AS XID)"},
	} {
		text := tc.input
		for round := range 5 {
			parsed, err := parser.Parse("SELECT " + text)
			if err != nil {
				t.Fatal(err)
			}
			b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			raw := parsed[0].(*parser.Select).Targets[0]
			node, err := b.Bind(raw, expressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			text, err = (scalarRenderer{}).sql(ctx, e.CatalogRead(), node)
			if err != nil || text != tc.want {
				t.Fatal("identifier literal replay", tc.input, round, text, tc.want, err)
			}
			if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
				t.Fatal(err)
			}
			renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.Capture.Literals, castUsages: b.Capture.CastUsages}, frame: b.FrameID()}
			text, err = renderer.expressionSQL(ctx, e.CatalogRead(), raw, viewLiteralPath{Clause: analyze.ViewTargetLiteral})
			if err != nil || text != tc.want {
				t.Fatal("identifier view replay", tc.input, round, text, tc.want, err)
			}
		}
	}
}

func TestIdentifierArrayAnnotationAdmission(t *testing.T) {
	ctx := context.Background()
	e := New()
	for _, typ := range []string{"xid", "cid", "xid8"} {
		for _, input := range []string{"NULL", "'{}'", "$1"} {
			parsed, err := parser.Parse("SELECT " + input + "::pg_catalog." + typ + "[]")
			if err != nil {
				t.Fatal(err)
			}
			b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			_, err = b.Bind(parsed[0].(*parser.Select).Targets[0], expressionPolicy{Context: analyze.ProjectionContext})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "unsupported column type" {
				t.Fatal("identifier array annotation must reject before datum/NULL construction", typ, input, err)
			}
		}
	}
}
