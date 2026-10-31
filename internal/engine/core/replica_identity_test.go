package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func replicaUnitFixture(t *testing.T) (*core.Engine, *session.Session, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	name := enginetest.PreparedBoundaryName("ri_unit")
	enginetest.PreparedUnitStatement(t, e, s, &parser.CreateTable{Name: name, Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "k", TypeName: parser.TypeName{Name: "int4"}, Constraints: []parser.ColumnConstraint{{NotNull: true}}}},
		{Column: &parser.Column{Name: "p", TypeName: parser.TypeName{Name: "text"}}},
	}})
	for _, n := range []string{"ri_a", "ri_b", "ri_c"} {
		enginetest.PreparedUnitStatement(t, e, s, &parser.CreateIndex{Name: parser.Identifier{Text: n}, Table: name, Method: "btree", Unique: true, Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "k"}}}, Include: []parser.IndexKey{{Column: parser.Identifier{Text: "p"}}}})
	}
	enginetest.PreparedUnitStatement(t, e, s, &parser.Insert{Name: name, Rows: [][]parser.Expr{{enginetest.PreparedBoundaryInteger(1), parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "one"}}}}})
	return e, s, e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "ri_unit").Address)
}

func replicaUnitAlter(mode parser.ReplicaIdentityMode, index string) *parser.AlterTable {
	return &parser.AlterTable{Name: enginetest.PreparedBoundaryName("ri_unit"), Actions: []parser.AlterTableAction{{Action: parser.SetReplicaIdentity, ReplicaIdentity: mode, ReplicaIndex: parser.Identifier{Text: index}}}}
}

func replicaUnitIndex(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	a, ok := e.CatalogRead().LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if !ok {
		t.Fatal("missing index", name)
	}
	return e.CatalogRead().MustRelation(a)
}
