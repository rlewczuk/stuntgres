package proc

import (
	"fmt"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// ValidatePseudoSignature composes catalog safety and language admission.
func ValidatePseudoSignature(inputs []sqltype.Usage, outputs []sqltype.Usage, formals []Formal, language string, standard bool) error {
	if err := ValidatePseudoResultSafety(inputs, outputs); err != nil {
		return err
	}
	return ValidatePseudoLanguageSignature(inputs, outputs, formals, language, standard)
}

// ValidatePseudoResultSafety checks catalog-level determinability and INTERNAL
// safety before replacement compatibility. OUT-only slots cannot supply inputs.
func ValidatePseudoResultSafety(inputs, outputs []sqltype.Usage) error {
	fail := func(code, message, detail string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	}
	for _, out := range outputs {
		id := out.Type.ID()
		family := sqltype.PolymorphicFamily(id)
		if family != 0 {
			ranged := id == 3831 || id == 4537 || id == 5080 || id == 4538
			found := false
			for _, in := range inputs {
				i := in.Type.ID()
				if sqltype.PolymorphicFamily(i) == family && (!ranged || i == 3831 || i == 4537 || i == 5080 || i == 4538) {
					found = true
				}
			}
			if !found {
				members := "anyelement, anyarray, anynonarray, anyenum, anyrange, or anymultirange"
				if family == 2 { members = "anycompatible, anycompatiblearray, anycompatiblenonarray, anycompatiblerange, or anycompatiblemultirange" }
				if ranged {
					members = "anyrange or anymultirange"
					if family == 2 { members = "anycompatiblerange or anycompatiblemultirange" }
				}
				return fail("42P13", "cannot determine result data type", fmt.Sprintf("A result of type %s requires at least one input of type %s.", out.Type.Name(), members))
			}
		}
		if id == 2281 {
			found := false
			for _, in := range inputs { found = found || in.Type.ID() == 2281 }
			if !found { return fail("42P13", `unsafe use of pseudo-type "internal"`, "A result of type internal requires at least one input of type internal.") }
		}
	}
	return nil
}

// ValidatePseudoLanguageSignature follows replacement authority and compatibility,
// like the language's body validator.
func ValidatePseudoLanguageSignature(inputs, outputs []sqltype.Usage, formals []Formal, language string, standard bool) error {
	fail := func(code, message, detail string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	}
	pl := language == "plpgsql"
	for _, out := range outputs {
		id := out.Type.ID()
		if !sqltype.IsPseudo(id) || sqltype.PolymorphicFamily(id) != 0 || id == 2249 || id == sqltype.Void || pl && (id == 2279 || id == 3838) { continue }
		if pl { return fail("0A000", "PL/pgSQL functions cannot return type "+out.Type.DiagnosticName(), "") }
		return fail("42P13", "SQL functions cannot return type "+out.Type.DiagnosticName(), "")
	}
	count := len(inputs)
	if pl { count = len(formals) }
	for i := range count {
		var in sqltype.Usage
		if pl { in = formals[i].Usage } else { in = inputs[i] }
		id := in.Type.ID()
		if standard && sqltype.PolymorphicFamily(id) != 0 { return fail("42P13", "SQL function with unquoted function body cannot have polymorphic arguments", "") }
		if !sqltype.IsPseudo(id) || sqltype.PolymorphicFamily(id) != 0 || pl && id == 2249 { continue }
		if pl { return fail("0A000", "PL/pgSQL functions cannot accept type "+in.Type.DiagnosticName(), "") }
		return fail("42P13", "SQL functions cannot have arguments of type "+in.Type.DiagnosticName(), "")
	}
	return nil
}

// InvocationSignature belongs to a checked call, never its published declaration.
type InvocationSignature struct {
	Inputs []sqltype.Usage
	Result sqltype.Usage
}

func (s InvocationSignature) Equal(t InvocationSignature) bool {
	if s.Result != t.Result || len(s.Inputs) != len(t.Inputs) { return false }
	for i := range s.Inputs { if s.Inputs[i] != t.Inputs[i] { return false } }
	return true
}

func (d *Definition) Polymorphic() bool {
	if sqltype.PolymorphicFamily(d.Result.Type.ID()) != 0 { return true }
	for _, in := range d.Inputs { if sqltype.PolymorphicFamily(in.Type.ID()) != 0 { return true } }
	for _, formal := range d.Formals {
		if sqltype.PolymorphicFamily(formal.Usage.Type.ID()) != 0 { return true }
	}
	if d.Result.Type.IsComposite() {
		for i := range d.Result.Type.CompositeLen() {
			if sqltype.PolymorphicFamily(d.Result.Type.CompositeMember(i).Usage.Type.ID()) != 0 { return true }
		}
	}
	return false
}

