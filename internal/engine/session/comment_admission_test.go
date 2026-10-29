package session

import (
	"testing"

	"stuntgres/internal/parser"
)

func TestCommentExplicitUtilityAdmission(t *testing.T) {
	for _, kind := range []parser.CommentKind{parser.CommentTable, parser.CommentColumn, parser.CommentForeignTable, parser.CommentSchema, parser.CommentRole, parser.CommentSequence, parser.CommentExtension} {
		if ExplicitUtility(&parser.Comment{Kind: kind}) {
			t.Errorf("shipped comment kind %d remains excluded", kind)
		}
	}
	for _, stmt := range []parser.Statement{&parser.Comment{}, &parser.Comment{Kind: parser.CommentKind(255)}, &parser.CreateRole{}, &parser.CreateIndex{}, &parser.CreateView{}} {
		if !ExplicitUtility(stmt) {
			t.Errorf("unrelated/unknown utility %T was blanket admitted", stmt)
		}
	}
}
