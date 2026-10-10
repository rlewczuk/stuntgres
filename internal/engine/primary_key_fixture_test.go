package engine

import (
	"stuntgres/internal/parser"
	"testing"
)

func primaryKeyUnitName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}

func primaryKeyUnitCreate(name, constraint string) *parser.CreateTable {
	return &parser.CreateTable{Name: primaryKeyUnitName(name), Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "a", TypeName: parser.TypeName{Name: "integer"}}},
		{Column: &parser.Column{Name: "b", TypeName: parser.TypeName{Name: "text"}}},
		{Column: &parser.Column{Name: "secret", TypeName: parser.TypeName{Name: "text"}}},
		{Unique: &parser.UniqueConstraint{Primary: true, Name: parser.Identifier{Text: constraint}, Columns: []parser.Identifier{{Text: "a"}, {Text: "b"}}}},
	}}
}

func primaryKeyUnitInsert(a int64) *parser.Insert {
	return &parser.Insert{Name: primaryKeyUnitName("t"), Rows: [][]parser.Expr{{
		parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: a}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "protected"}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "private"}},
	}}}
}

// Exercise package entry points with independently built ASTs, not driver SQL.
// The resulting descriptors, backing roots and ACL identities are real objects.
func primaryKeyUnitFixture(t *testing.T) (*Engine, *Operation, *relationDescriptor, *relationDescriptor) {
	t.Helper()
	e := New()
	admin, adminDone := roleOperation(t, e, "postgres")
	t.Cleanup(adminDone)
	if _, err := e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: "pk_owner"}, DefaultLogin: true}, 0, nil); err != nil {
		t.Fatal(err)
	}
	owner, ownerDone := roleOperation(t, e, "pk_owner")
	t.Cleanup(ownerDone)
	if _, err := e.Execute(owner, primaryKeyUnitCreate("t", "pk"), 0, nil); err != nil {
		t.Fatal(err)
	}
	for _, a := range []int64{1, 2} {
		result, err := e.Execute(owner, primaryKeyUnitInsert(a), 0, nil)
		if err != nil || result.Count != 1 || result.Rows != nil {
			t.Fatal(result, err)
		}
	}
	parent := primaryKeyUnitRelation(t, e, "t")
	index := e.state.descriptors[parent.uniqueConstraints[0].Index].(*relationDescriptor)
	return e, owner, parent, index
}

func primaryKeyUnitRelation(t *testing.T, e *Engine, name string) *relationDescriptor {
	t.Helper()
	address, ok := e.state.names[NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: name}]
	if !ok {
		t.Fatal("missing relation", name)
	}
	return e.state.descriptors[address].(*relationDescriptor)
}
