package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/fts"
)

func TestTriggerBuiltinsPositions(t *testing.T) {
	dictionary, err := CompileSimpleDictionary(t.Context(), true, "english")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sources  []string
		want     string
		position int
	}{
		{[]string{"cat the", "dog"}, "'cat':1 'dog':3", 3},
		{[]string{"dog", "cat the"}, "'cat':2 'dog':1", 3},
		{[]string{"cat", "", "dog", "cat"}, "'cat':1,3 'dog':2", 3},
		{[]string{"the", "the"}, "", 2},
	} {
		var builder fts.VectorBuilder
		position := 0
		for _, source := range tc.sources {
			tokens, err := fts.ParseDocument(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			err = fts.NormalizeTokens(t.Context(), tokens, &position, func(token fts.DocumentToken) (fts.LexicalResult, error) {
				if token.ID == 12 {
					return fts.LexicalResult{}, nil
				}
				return dictionary.Lexize(t.Context(), token.Text)
			}, func(p fts.LexicalPosition) {
				for _, word := range p.Lexemes {
					builder.Add(word.Text, p.Position)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		vector, err := builder.Finish(t.Context())
		if err != nil || vector.String() != tc.want || position != tc.position {
			t.Fatal(tc.sources, vector, position, err)
		}
	}
}

func TestTriggerBuiltinsCancellationPositions(t *testing.T) {
	dictionary, err := CompileSimpleDictionary(t.Context(), true, "english")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tokens, err := fts.ParseDocument(ctx, "cat the dog")
	if err != nil {
		t.Fatal(err)
	}
	position := 0
	var builder fts.VectorBuilder
	err = fts.NormalizeTokens(ctx, tokens, &position, func(token fts.DocumentToken) (fts.LexicalResult, error) {
		if token.ID == 12 {
			return fts.LexicalResult{}, nil
		}
		value, err := dictionary.Lexize(ctx, token.Text)
		if token.Text == "the" {
			cancel()
		}
		return value, err
	}, func(p fts.LexicalPosition) {
		for _, word := range p.Lexemes {
			builder.Add(word.Text, p.Position)
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("interior normalization was not canceled", err)
	}
	if vector, err := builder.Finish(ctx); vector != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled partial vector published", vector, err)
	}
}
