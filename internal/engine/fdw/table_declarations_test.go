package fdw

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestFDWStageTableDeclarationConstraints(t *testing.T) {
	keyword := parser.Position{Character: 31}
	named := parser.Position{Character: 19}
	for _, tc := range []struct {
		name              string
		element           parser.TableElement
		message, position string
	}{
		{"primary", parser.TableElement{Unique: &parser.UniqueConstraint{Primary: true, Pos: keyword}}, "primary key", "31"},
		{"unique", parser.TableElement{Unique: &parser.UniqueConstraint{Pos: named}}, "unique", "19"},
		{"foreign", parser.TableElement{ForeignKey: &parser.ForeignKeyConstraint{Pos: keyword}}, "foreign key", "31"},
		{"named_foreign", parser.TableElement{ForeignKey: &parser.ForeignKeyConstraint{Pos: keyword, NamedDeclarationPos: named}}, "foreign key", "19"},
		{"exclusion", parser.TableElement{Exclusion: &parser.ExclusionConstraint{Pos: keyword}}, "exclusion", "31"},
		{"named_exclusion", parser.TableElement{Exclusion: &parser.ExclusionConstraint{Pos: keyword, NamedDeclarationPos: named}}, "exclusion", "19"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckTableDeclaration(&parser.CreateTable{Elements: []parser.TableElement{tc.element}})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != tc.message+" constraints are not supported on foreign tables" || diagnostic.Position != tc.position {
				t.Fatalf("diagnostic=%+v error=%v", diagnostic, err)
			}
			if tc.element.Exclusion == nil {
				column := &parser.Column{Constraints: []parser.ColumnConstraint{{Unique: tc.element.Unique, ForeignKey: tc.element.ForeignKey}}}
				if err := CheckAddedColumn(column); !errors.As(err, &diagnostic) || diagnostic.Position != tc.position || diagnostic.Message != tc.message+" constraints are not supported on foreign tables" {
					t.Fatalf("added-column diagnostic=%+v error=%v", diagnostic, err)
				}
			}
		})
	}
	identity := &parser.Column{Constraints: []parser.ColumnConstraint{{Identity: &parser.IdentityDefinition{}}}}
	if err := CheckAddedColumn(identity); err != nil {
		t.Fatal(err)
	}
}

func TestFDWStageTableLikeSyntaxOwnership(t *testing.T) {
	options := []parser.LikeOption{{Name: "all", Including: true}, {Name: "defaults"}}
	like := &parser.TableLike{Options: options}
	raw := &parser.CreateTable{Elements: []parser.TableElement{{Like: like}}}
	normalized := TableSyntax(raw)
	if normalized == raw || normalized.Elements[0].Like == like || !reflect.DeepEqual(like.Options, options) || len(like.Options) != 2 {
		t.Fatal("normalization changed caller-owned LIKE syntax")
	}
	want := append(append([]parser.LikeOption(nil), options...), parser.LikeOption{Name: "indexes"}, parser.LikeOption{Name: "identity"}, parser.LikeOption{Name: "storage"}, parser.LikeOption{Name: "compression"})
	if !reflect.DeepEqual(normalized.Elements[0].Like.Options, want) {
		t.Fatalf("normalized=%+v want=%+v", normalized.Elements[0].Like.Options, want)
	}
	plain := &parser.CreateTable{Elements: []parser.TableElement{{Column: &parser.Column{Name: "id"}}}}
	if TableSyntax(plain) != plain {
		t.Fatal("ordinary declaration was copied")
	}
}
