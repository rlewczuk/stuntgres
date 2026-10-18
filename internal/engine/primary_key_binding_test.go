package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPrimaryKeyExplicitRetention(t *testing.T) {
	for _, primaryFirst := range []bool{false, true} {
		p := parser.TableElement{Unique: &parser.UniqueConstraint{Primary: true, Columns: []parser.Identifier{{Text: "a"}}}}
		u := parser.TableElement{Unique: &parser.UniqueConstraint{Name: parser.Identifier{Text: "chosen"}, Columns: []parser.Identifier{{Text: "a"}}}}
		elements := []parser.TableElement{u, p}
		if primaryFirst {
			elements = []parser.TableElement{p, u}
		}
		elements = append(elements, parser.TableElement{Unique: &parser.UniqueConstraint{NullsNotDistinct: true, Columns: []parser.Identifier{{Text: "a"}}}})
		bound, err := bindCreateUniques(&parser.CreateTable{Name: primaryKeyUnitName("t"), Elements: elements}, map[string]int{"a": 0}, nil, catalog.Read{})
		if err != nil || len(bound) != 2 || !bound[0].Primary || bound[0].Name != "chosen" || bound[1].Primary || !bound[1].NullsNotDistinct || !reflect.DeepEqual(bound[0].Attributes, []int32{1}) {
			t.Fatal(primaryFirst, bound, err)
		}
	}
	first := parser.TableElement{Unique: &parser.UniqueConstraint{Primary: true, Columns: []parser.Identifier{{Text: "a"}}}}
	second := parser.TableElement{Unique: &parser.UniqueConstraint{Primary: true, Columns: []parser.Identifier{{Text: "missing"}}, Pos: parser.Position{Character: 37}}}
	bound, err := bindCreateUniques(&parser.CreateTable{Name: primaryKeyUnitName("t"), Elements: []parser.TableElement{first, second}}, map[string]int{"a": 0}, nil, catalog.Read{})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P16", Message: `multiple primary keys for table "t" are not allowed`, Position: "37"})
	if bound != nil {
		t.Fatal("partial declarations escaped", bound)
	}
}

func TestPrimaryKeyNotNullBinding(t *testing.T) {
	types := indexClausesTypes()
	columns := []ColumnDescriptor{{Name: "z", Usage: types[sqltype.Int4].Usage(), Nullable: true, NullabilityKnown: true}, {Name: "b", Usage: types[sqltype.Int4].Usage(), Nullable: true, NullabilityKnown: true}, {Name: "a", Usage: types[sqltype.Int4].Usage(), Nullable: true, NullabilityKnown: true}}
	s := &parser.CreateTable{Name: primaryKeyUnitName("t"), Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "z", Constraints: []parser.ColumnConstraint{{NotNull: true, Name: parser.Identifier{Text: "explicit"}}}}},
		{Column: &parser.Column{Name: "b"}},
		{Column: &parser.Column{Name: "a", Constraints: []parser.ColumnConstraint{{NotNull: false}}}},
		{Unique: &parser.UniqueConstraint{Primary: true, Columns: []parser.Identifier{{Text: "a"}, {Text: "b"}}}},
	}}
	declarations := make([]createColumnDeclaration, len(columns))
	for i := range columns {
		var err error
		declarations[i], err = bindCreateColumnDeclaration(s.Elements[i].Column, "t")
		if err != nil {
			t.Fatal(err)
		}
	}
	index, err := bindCreateColumnIndex(columns)
	if err != nil {
		t.Fatal(err)
	}
	uniques, err := bindCreateUniques(s, index, nil, catalog.Read{})
	if err != nil {
		t.Fatal(err)
	}
	nn, err := bindCreateNotNull(s, columns, declarations, nil, uniques, index)
	if err != nil || len(nn) != 3 || nn[0].Attribute != 1 || nn[0].Name != "explicit" || nn[1].Attribute != 3 || nn[2].Attribute != 2 {
		t.Fatal("existing NN then PK key order", nn, err)
	}
	for _, column := range columns {
		if column.Nullable || !column.NullabilityKnown {
			t.Fatal(column)
		}
	}
	column := &parser.Column{Name: "a", Constraints: []parser.ColumnConstraint{
		{Unique: &parser.UniqueConstraint{Primary: true, Name: parser.Identifier{Text: "pk"}}},
		{NotNull: true, Name: parser.Identifier{Text: "nn"}},
	}}
	declaration, err := bindCreateColumnDeclaration(column, "t")
	if err != nil || !declaration.columnNotNull || declaration.columnName != "nn" || column.Constraints[0].Name.Text != "" || column.Constraints[0].Unique.Name.Text != "pk" {
		t.Fatal("separate NN and PK names", column, declaration, err)
	}
	column.Constraints = append([]parser.ColumnConstraint{{NotNull: false}}, parser.ColumnConstraint{Unique: &parser.UniqueConstraint{Primary: true}, Pos: parser.Position{Character: 27}})
	_, err = bindCreateColumnDeclaration(column, "t")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position != "27" {
		t.Fatal(err)
	}
}
