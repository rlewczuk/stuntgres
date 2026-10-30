package dump

import (
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestXMLRestoreScalarGuard(t *testing.T) {
	null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	parameter := parser.ParameterExpr{Number: 1, Pos: parser.Position{Offset: 37, Character: 38}}
	for _, stored := range []bool{false, true} {
		for _, raw := range []parser.Expr{
			parser.XMLParseExpr{Child: null, Document: true},
			parser.XMLSerializeExpr{Child: parser.XMLParseExpr{Child: null}, Target: parser.TypeName{Name: "text"}},
			parser.XMLDocumentPredicateExpr{Child: null},
			parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "xmlexists"}}}, Args: []parser.Expr{null, null}},
		} {
			if err := RestoreScalar(raw, stored); err != nil {
				t.Fatal("lossless XML recipe rejected", err)
			}
		}
		for _, raw := range []parser.Expr{
			parser.XMLSerializeExpr{Child: parser.XMLParseExpr{Child: parser.ConditionalExpr{Args: []parser.Expr{null, parameter}}}, Target: parser.TypeName{Name: "text"}},
			parser.XMLDocumentPredicateExpr{Child: parameter},
			parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "xmlexists"}}}, Args: []parser.Expr{parameter, null}},
			parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "xmlexists"}}}, Args: []parser.Expr{null, parameter}},
		} {
			err := RestoreScalar(raw, stored)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" || diagnostic.Position != "38" {
				t.Fatal("strict NULL hid forbidden restore parameter", raw, err)
			}
		}
	}
}
