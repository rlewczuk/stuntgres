package reference_test

import (
	"strings"
	"testing"
)

func TestReferencePorterSnowballBoundaries(t *testing.T) {
	// Released whole-token steps, ASCII-only lowercasing in the fixture C locale.
	porterCorpus(t, [][2]string{
		{"RATS", "rat"}, {"SKIES", "ski"}, {"ONLY", "onli"},
		{"café", "café"}, {"cafés", "café"}, {"CAFÉS", "cafÉ"}, {"cafe\u0301s", "cafe\u0301"},
		{"cats!", "cats!"}, {"'skies", "'ski"}, {"sky's", "sky'"},
		{strings.Repeat("a", 997) + "ies", strings.Repeat("a", 997) + "i"},
		{strings.Repeat("A", 998) + "ies", strings.Repeat("a", 998) + "ies"},
		{strings.Repeat("é", 498) + "aies", strings.Repeat("é", 498) + "ai"},
		{strings.Repeat("é", 499) + "ies", strings.Repeat("é", 499) + "ies"},
	})
}
