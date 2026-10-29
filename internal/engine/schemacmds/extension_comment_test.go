package schemacmds_test

import (
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// A test-owned metadata view delegates the real released installer; it is never
// registered as a shipped provider and its candidate is never published.
type extensionCommentControl struct{ comment *string }

func (p *extensionCommentControl) Control() catalog.ExtensionControl {
	c := extension.StockUnaccent.Control()
	c.Comment = p.comment
	return c
}
func (*extensionCommentControl) Versions() catalog.ExtensionVersions {
	return extension.StockUnaccent.Versions()
}
func (*extensionCommentControl) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return extension.StockUnaccent.ValidateExtension(v, e)
}
func (*extensionCommentControl) Stage(s extension.Stage, e *catalog.Extension, owner authorization.PrincipalRef) error {
	return extension.StockUnaccent.Stage(s, e, owner)
}

func TestExtensionCommentInitialMetadata(t *testing.T) {
	for _, value := range []string{"absent", "", "initial 雪"} {
		t.Run(value, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			metadata := &extensionCommentControl{}
			want := value
			if value == "absent" {
				want = ""
			} else {
				metadata.comment = &value
			}
			original := e.Catalog.Extensions["unaccent"]
			e.Catalog.Extensions["unaccent"] = metadata
			defer func() { e.Catalog.Extensions["unaccent"] = original }()
			e.Mu.Lock()
			stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer e.DiscardCatalogStage(stage)
			view := e.StageRead(stage)
			ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
			if ext.Comment != want || pgcatalog.DescriptorComment(ext, 0) != want || pgcatalog.DescriptorComment(ext, 1) != "" {
				t.Fatal("initial comment metadata", ext.Comment, want)
			}
			if len(ext.Members) == 0 || ext.Installation == nil || ext.Installation.Provider != "unaccent" {
				t.Fatal("real installation was not staged")
			}
			produce, ok := pgcatalog.ProducerFor(2609)
			if !ok {
				t.Fatal("missing pg_description producer")
			}
			rows, err := produce(pgcatalog.Input{Context: context.Background(), Catalog: view, Source: view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2609})})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, row := range rows {
				object, err := row[0].OID()
				if err != nil {
					t.Fatal(err)
				}
				class, err := row[1].OID()
				if err != nil {
					t.Fatal(err)
				}
				if ids.OID(object) != ext.Address().ObjectID || class != 3079 {
					continue
				}
				sub, err := row[2].Integer()
				if err != nil {
					t.Fatal(err)
				}
				text, err := row[3].Text()
				if err != nil {
					t.Fatal(err)
				}
				if sub != 0 || text != want || want == "" {
					t.Fatal("invalid installed comment tuple", row)
				}
				count++
			}
			expected := 0
			if want != "" {
				expected = 1
			}
			if count != expected {
				t.Fatal("installed comment row multiplicity", count, expected)
			}
			oid, err := sqltype.OIDValue(view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 26}).SQLType, uint32(ext.Address().ObjectID))
			if err != nil {
				t.Fatal(err)
			}
			args := [2]sqltype.Value{oid, sqltype.String(view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 19}).SQLType, "pg_extension")}
			text, err := regobj.CommentText(view, e.Roles(), functions.ObjectDescription, args)
			if err != nil || text != want {
				t.Fatal("initial inquiry text (empty is SQL NULL)", text, err)
			}
			if metadata.comment != nil {
				*metadata.comment = "mutated caller metadata"
			}
			if ext.Comment != want {
				t.Fatal("installed comment retained mutable control storage")
			}
			if e.CatalogRead().Get(ext.Address()) != nil {
				t.Fatal("staging published extension")
			}
		})
	}
}
