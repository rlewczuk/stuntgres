package sqltype

import (
	"bytes"
	"testing"
)

func TestPLpgSQLCursorCodec(t *testing.T) {
	types := testTypes()
	d := types[Refcursor]
	if d == nil || !d.Concrete() || d.ID() != 1790 || d.AssociatedArray() != 2201 || d.DatabaseName() != "REFCURSOR" || d.Usage().Collation.ID != 0 || IsCharacter(d.ID()) {
		t.Fatal("refcursor scalar identity", d)
	}
	if types[2201].Concrete() {
		t.Fatal("scalar admission silently enabled arrays")
	}
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Category != "U" || facts.Collation != 0 || facts.Input != 46 || facts.Output != 47 || facts.Receive != 2414 || facts.Send != 2415 {
		t.Fatal("refcursor catalog facts", facts, ok)
	}
	for _, name := range []string{"", "portal", "quoted'portal", "游标"} {
		value, err := d.Input(t.Context(), name, -1)
		if err != nil {
			t.Fatal(err)
		}
		text, err := value.Text()
		if err != nil || text != name || value.Type() != d {
			t.Fatal("typed input", value, err)
		}
		output, err := d.Output(t.Context(), value)
		if err != nil || output != name {
			t.Fatal("text codec", output, err)
		}
		encoded, err := d.AppendBinaryWithEnvironment(t.Context(), nil, value, OutputEnvironment{})
		if err != nil || !bytes.Equal(encoded, []byte(name)) {
			t.Fatal("binary output", encoded, err)
		}
		decoded, err := d.BinaryInputWithEnvironment(t.Context(), encoded, -1, InputEnvironment{})
		if err != nil || decoded != value {
			t.Fatal("binary round trip", decoded, err)
		}
		if !SameDatum(value, decoded) || SameDatum(value, String(d, name+"!")) || SameDatum(value, Null(d)) || SameDatum(value, String(types[Text], name)) {
			t.Fatal("physical cursor datum identity", name)
		}
		literal, err := d.SQLLiteral(t.Context(), value)
		if err != nil || len(literal) < 2 || literal[0] != '\'' || literal[len(literal)-1] != '\'' {
			t.Fatal("stored literal", literal, err)
		}
	}
	for _, bad := range []string{"a\x00b", string([]byte{0xff})} {
		if _, err := d.Input(t.Context(), bad, -1); err == nil {
			t.Fatal("invalid text accepted")
		}
		if _, err := d.BinaryInputWithEnvironment(t.Context(), []byte(bad), -1, InputEnvironment{}); err == nil {
			t.Fatal("invalid binary text accepted")
		}
	}
	if !Null(d).IsNull() || Null(d).Type() != d {
		t.Fatal("typed NULL")
	}
	if !SameDatum(Null(d), Null(d)) || SameDatum(Null(d), Null(types[Text])) {
		t.Fatal("cursor NULL datum identity")
	}
	for _, pair := range [][2]*Descriptor{{d, types[Text]}, {types[Text], d}} {
		if _, err := PlanCoercion(pair[0].Usage(), pair[1].Usage(), Explicit); err != nil {
			t.Fatal("explicit I/O cast", err)
		}
		if _, err := PlanCoercion(pair[0].Usage(), pair[1].Usage(), Implicit); err == nil {
			t.Fatal("broad implicit text cast")
		}
	}
	if err := d.CheckBinaryInput(); err != nil {
		t.Fatal(err)
	}
	if err := d.CheckBinaryOutput(); err != nil {
		t.Fatal(err)
	}
}
