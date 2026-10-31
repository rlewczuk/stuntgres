package enginetest

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// PublishStage validates stage and publishes it outside an operation.
func PublishStage(t testing.TB, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

// SimpleDictionary is CREATE TEXT SEARCH DICTIONARY name (template simple, english stopwords).
func SimpleDictionary(name string) *parser.CreateDictionary {
	return &parser.CreateDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "template"}, Value: "simple"}, Supplied: true}, {CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}, Value: "english"}, Supplied: true}}}
}

// PublishOperationStage validates stage and publishes it through op.
func PublishOperationStage(t testing.TB, e *core.Engine, op *session.Operation, stage *core.CatalogStage) {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalogOperation(op, nil, p, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

// SnowballDictionary is CREATE TEXT SEARCH DICTIONARY name for the english Snowball stemmer.
func SnowballDictionary(name string) *parser.CreateDictionary {
	return &parser.CreateDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "template"}, Value: "snowball"}, Supplied: true}, {CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "language"}, Value: "english"}, Supplied: true}, {CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}, Value: "english"}, Supplied: true}}}
}
