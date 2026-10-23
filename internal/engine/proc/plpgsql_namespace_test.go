package proc

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLFormalNamespaceCompiler(t *testing.T) {
	integer := plUnitInteger(t)
	for _, tc := range []struct{ name, input, output, duplicate string }{
		{"in_out", "x", "x", "x"},
		{"own_positional", "$1", "result", "$1"},
		{"later_positional", "$2", "result", "$2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Definition{Name: "formal_namespace", Result: integer.Usage(), Source: "BEGIN $2 := $1+1; RETURN; END"}
			// Shared SQL projections remain legal; only PL namespace admission rejects.
			if err := d.SetFormals([]Formal{{Mode: "i", Name: tc.input, Usage: integer.Usage()}, {Mode: "o", Name: tc.output, Usage: integer.Usage()}}); err != nil {
				t.Fatal("shared formal layout rejected legal SQL projection", err)
			}
			raw, err := parser.ParsePLpgSQL(d.Source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = CompilePL(d, raw, false, func(parser.PLType) (sqltype.Usage, bool, error) { return integer.Usage(), false, nil })
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42P13" || diagnostic.Message != fmt.Sprintf("parameter name %q used more than once", tc.duplicate) {
				t.Fatalf("PL duplicate formal namespace: %v", err)
			}
		})
	}
}

func TestPLpgSQLFormalNamespaceValidation(t *testing.T) {
	integer := plUnitInteger(t)
	d := &Definition{Name: "formal_namespace", Result: integer.Usage(), Source: "BEGIN $2 := $1+1; RETURN; END"}
	formals := []Formal{{Mode: "i", Name: "x", Usage: integer.Usage()}, {Mode: "o", Name: "y", Usage: integer.Usage()}}
	if err := d.SetFormals(formals); err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(d.Source)
	if err != nil {
		t.Fatal(err)
	}
	p, err := CompilePL(d, raw, false, func(parser.PLType) (sqltype.Usage, bool, error) { return integer.Usage(), false, nil })
	if err != nil {
		t.Fatal(err)
	}
	d.Kind, d.PLSyntax, d.PL = PLpgSQLBody, raw, p
	if err := p.ValidateDefinition(d); err != nil {
		t.Fatal("valid named formal namespace", err)
	}
	for _, name := range []string{"duplicate", "own_positional", "later_positional", "missing", "redirected"} {
		t.Run(name, func(t *testing.T) {
			broken, definition, root := *p, *d, *p.Root
			root.Bindings = map[string]int{"$1": 0, "$2": 1, "x": 0, "y": 1}
			broken.Root, definition.PL = &root, &broken
			switch name {
			case "duplicate":
				// Model a later OUT installation overwriting the earlier IN name,
				// while retaining legal projections and both positional slots.
				ambiguous := append([]Formal(nil), formals...)
				ambiguous[1].Name = "x"
				if err := definition.SetFormals(ambiguous); err != nil {
					t.Fatal(err)
				}
				broken.Variables = append([]PLVariable(nil), p.Variables...)
				broken.Variables[1].Name = "x"
				delete(root.Bindings, "y")
				root.Bindings["x"] = 1
			case "own_positional", "later_positional":
				ambiguous := append([]Formal(nil), formals...)
				ambiguous[0].Name = "$1"
				if name == "later_positional" {
					ambiguous[0].Name = "$2"
				}
				if err := definition.SetFormals(ambiguous); err != nil {
					t.Fatal(err)
				}
				broken.Variables = append([]PLVariable(nil), p.Variables...)
				broken.Variables[0].Name = ambiguous[0].Name
				delete(root.Bindings, "x")
			case "missing":
				delete(root.Bindings, "x")
			case "redirected":
				root.Bindings["x"] = 1
			}
			if err := broken.ValidateDefinition(&definition); err == nil {
				t.Fatal("invalid named formal namespace accepted")
			}
		})
	}
}
