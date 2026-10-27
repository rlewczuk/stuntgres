package extension

import (
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestEarthRoutineDeclarationBodies(t *testing.T) {
	// Exercise every actual installed declaration recipe, including quoted
	// installation schema substitution; no routine binding or execution occurs.
	for op := sqltype.EarthRadiusOperation; op <= sqltype.EarthLongitude; op++ {
		t.Run(op.Name(), func(t *testing.T) {
			body, err := earthDeclarationBody(op, "cube schema")
			if err != nil {
				t.Fatal(err)
			}
			if body == nil {
				t.Fatal("missing released SQL-standard declaration body")
			}
			output, err := body.StructuralOutput(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(output, "{RoutineDeclarationBody {parser.Select ") {
				t.Fatalf("not an owned parsed SELECT body: %s", output)
			}
			if op >= sqltype.EarthLL && !strings.Contains(output, `Text "cube schema"`) {
				t.Fatalf("lost installation schema identity: %s", output)
			}
		})
	}
}
