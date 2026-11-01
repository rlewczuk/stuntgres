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
		for _, name := range []string{"xpath", "xpath_exists"} {
			for _, arity := range []int{2, 3} {
				for _, qualified := range []bool{false, true} {
					parts := []parser.Identifier{{Text: name}}
					if qualified {
						parts = append([]parser.Identifier{{Text: "pg_catalog"}}, parts...)
					}
					args := []parser.Expr{null, parser.XMLParseExpr{Child: null, Document: true}}
					if arity == 3 {
						args = append(args, parser.ArrayExpr{Elements: []parser.Expr{
							parser.ArrayExpr{Elements: []parser.Expr{null, null}},
						}})
					}
					recipe := parser.FunctionExpr{Name: parser.ObjectName{Parts: parts}, Args: args}
					if err := RestoreScalar(recipe, stored); err != nil {
						t.Fatal("lossless XPath recipe rejected", recipe, stored, err)
					}
					for operand := range args {
						forbiddenOperands := []parser.Expr{parameter}
						switch operand {
						case 0:
							forbiddenOperands = append(forbiddenOperands, parser.ConditionalExpr{Args: []parser.Expr{null, parameter}})
						case 1:
							forbiddenOperands = append(forbiddenOperands, parser.XMLParseExpr{Child: parser.ConditionalExpr{Args: []parser.Expr{null, parameter}}, Document: true})
						case 2:
							for _, pair := range [][]parser.Expr{{parameter, null}, {null, parameter}} {
								forbiddenOperands = append(forbiddenOperands, parser.ArrayExpr{Elements: []parser.Expr{
									parser.ArrayExpr{Elements: pair},
								}})
							}
						}
						for _, forbidden := range forbiddenOperands {
							// Every other operand is strict NULL: neither strictness
							// nor recursive syntax may hide a forbidden parameter.
							children := make([]parser.Expr, arity)
							for i := range children {
								children[i] = null
							}
							children[operand] = forbidden
							raw := parser.FunctionExpr{Name: recipe.Name, Args: children}
							err := RestoreScalar(raw, stored)
							var diagnostic *sqlstate.Error
							if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" || diagnostic.Message != "parameters are not allowed in restore" || diagnostic.Position != "38" {
								t.Fatal("strict NULL hid forbidden XPath restore parameter", raw, stored, err)
							}
						}
					}
				}
			}
		}
	}
}
