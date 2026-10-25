package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAccessMethodExclusionDescriptorPrecedence(t *testing.T) {
	for _, name := range []string{"gin", "brin"} {
		for _, kind := range []string{"unresolved", "table"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				e := New()
				if kind == "table" {
					accessMethodPublish(t, e, accessMethodUnit(t, e, name, catalog.HeapAccessMethod, 3))
				}
				op, auth := conversionUnitAuth(t, e)
				stage := e.BeginCatalogStage(auth.Catalog)
				defer e.DiscardCatalogStage(stage)
				raw := &parser.ExclusionConstraint{Index: parser.CreateIndex{Method: name, Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "n"}}}}, Operators: []parser.ObjectName{ddlHostName("=")}}
				e.Mu.Lock()
				err := index.StageExclusion(TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, sqltype.InputEnvironment{}, &catalog.Relation{}, raw, false)
				e.Mu.Unlock()
				code, message := "0A000", `access method "`+name+`" does not support exclusion constraints`
				if kind == "table" {
					code, message = "55000", `access method "`+name+`" is not of type INDEX`
				}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || diagnostic.Position != "" || stage.DescriptorCount() != 0 {
					t.Fatal("unresolved/kind boundary lost precedence or staged a prefix", err)
				}
			})
		}
		t.Run(name, func(t *testing.T) {
			e := New()
			if _, exists := e.CatalogRead().LookupAccessMethod(name); exists {
				t.Fatal("fixture requires an absent native builtin name")
			}
			// These are genuinely registered btree descriptors and method-owned
			// classes, not implementations of the absent stock GIN/BRIN algorithms.
			method, family, class, support := accessMethodClassUnit(t, e, name)
			view := e.CatalogRead()
			builtin, ok := view.OperatorClass(1978)
			if !ok {
				t.Fatal("missing ordinary int4 class")
			}
			equality, ok := view.FamilyOperator(builtin.Family, 23, 23, 3, 's')
			if !ok {
				t.Fatal("missing actual int4 equality member")
			}
			member := *equality
			member.ObjectAddress.ObjectID = catalogUnitOID(t, e)
			member.ObjectRevision = 1
			member.Method, member.Family, member.InternalOwner = method.Address().ObjectID, family.Address().ObjectID, class.Address()
			accessMethodPublish(t, e, &member)
			op, auth := conversionUnitAuth(t, e)
			before := e.CatalogRead()
			if actual, ok := before.LookupAccessMethod(name); !ok || actual != method || actual.Handler != 330 {
				t.Fatal("missing registered descriptor authority", actual)
			}
			physical, ok := method.IndexImplementation()
			if !ok || physical != catalog.BtreeMethod {
				t.Fatal("custom SQL name did not retain real btree implementation")
			}
			stage := e.BeginCatalogStage(before)
			defer e.DiscardCatalogStage(stage)
			h := TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}
			typ, _ := before.LookupType(sqltype.Int4)
			raw := &parser.ExclusionConstraint{
				Name:      parser.Identifier{Text: "am_exclusion_backing"},
				Index:     parser.CreateIndex{Method: name, Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "n"}}}},
				Operators: []parser.ObjectName{ddlHostName("pg_catalog", "=")},
			}
			e.Mu.Lock()
			parent, err := table.NewRelation(h, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "am_exclusion_rows", auth.State.CurrentUser, []catalog.Column{{Name: "n", Usage: typ.Usage()}}, nil)
			if err == nil {
				err = e.StageRowTypeLocked(stage, parent)
			}
			if err == nil {
				err = stage.Create(parent)
			}
			if err == nil {
				err = index.StageExclusion(h, auth, sqltype.InputEnvironment{}, parent, raw, false)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal("registered btree descriptor must override absent-builtin spelling", err)
			}
			if len(parent.Constraints.Exclusion) != 1 {
				t.Fatal("exclusion constraint not staged")
			}
			backing := h.StageRead().MustRelation(parent.Constraints.Exclusion[0].Index)
			definition := backing.SQLIndex
			if definition == nil || definition.Method != catalog.BtreeMethod || definition.MethodIdentity() != method.Address().ObjectID || len(definition.Keys) != 1 || len(definition.Exclusion) != 1 {
				t.Fatal("exclusion backing lost actual method authority", definition)
			}
			bound := definition.Keys[0].Class
			if bound.OID() != uint32(class.Address().ObjectID) || bound.FamilyOID() != uint32(family.Address().ObjectID) || bound.Support(1).Routine != 351 || bound.Support(1).Member != uint32(support.Address().ObjectID) {
				t.Fatal("exclusion used a builtin class or fabricated support", bound)
			}
			if e.CatalogRead().State() != before.State() {
				t.Fatal("private exclusion stage published catalog changes")
			}
		})
	}
}
