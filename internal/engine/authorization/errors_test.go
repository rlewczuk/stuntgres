package authorization

import (
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestAuthorizationDiagnosticCharacterPosition(t *testing.T) {
	for _, tc := range []struct {
		name     string
		position parser.Position
		want     string
	}{
		{"ascii", parser.Position{Offset: 13, Character: 14}, "14"},
		{"multibyte_prefix", parser.Position{Offset: 17, Character: 14}, "14"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := insertError("42601", "conflicting or redundant options", tc.position)
			if got := err.(*sqlstate.Error).Position; got != tc.want {
				t.Fatalf("diagnostic position = %q, want character position %q", got, tc.want)
			}
		})
	}
}
