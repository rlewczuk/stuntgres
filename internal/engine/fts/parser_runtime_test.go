package fts

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestFTSHeadlineParserCapability(t *testing.T) {
	for _, tc := range []struct {
		headline uint32
		message  string
	}{
		{0, "text search parser does not support headline creation"},
		{99999, "text search parser callback has no native implementation"},
		{3720, ""},
	} {
		runtime := NewParserRuntime(3717, 3718, 3719, tc.headline, 3721)
		err := runtime.CheckHeadline(t.Context())
		if tc.message == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != tc.message {
				t.Fatal(err)
			}
		}
		// HEADLINE capability must not gate existing parser/token operations.
		if tokens, err := runtime.Parse(t.Context(), "Cat"); err != nil || len(tokens) != 1 || tokens[0].Text != "Cat" {
			t.Fatal(tokens, err)
		}
		if _, err := runtime.TokenTypes(t.Context()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := runtime.CheckHeadline(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
