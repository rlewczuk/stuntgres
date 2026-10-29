package extension

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/proc"
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

func TestEarthDeclarationCubePlacement(t *testing.T) {
	for _, op := range []sqltype.EarthOperation{sqltype.EarthLL, sqltype.EarthLatitude, sqltype.EarthLongitude} {
		for _, schema := range []string{"public", `Cube " historical`, "pg_catalog"} {
			body, err := earthDeclarationBody(op, schema)
			if err != nil {
				t.Fatal(err)
			}
			got, err := proc.EarthDeclarationCubeSchema(body, op)
			if err != nil || got != schema {
				t.Fatalf("%s placement=%q, %v; want %q", op.Name(), got, err, schema)
			}
			if _, err := proc.EarthDeclarationCubeSchema(body, sqltype.EarthRadiusOperation); err == nil {
				t.Fatal("unconstrained operation acquired cube placement")
			}
		}
	}
	for _, source := range []string{
		`SELECT degrees(atan2(a.cube_ll_coord($1,2),b.cube_ll_coord($1,1)));`,
		`SELECT degrees(atan2(cube_ll_coord($1,2),cube_ll_coord($1,1)));`,
		`SELECT degrees(atan2(a.cube_ll_coord($1),a.cube_ll_coord($1,1)));`,
		`SELECT degrees(atan2(a.cube_ll_coord($1,2),a.other($1,1)));`,
		`SELECT a.cube_ll_coord($1,1);`,
		`SELECT degrees(atan2(a.cube_ll_coord($1,2),a.cube_ll_coord($1,1))) FROM t;`,
	} {
		body, err := proc.NewRoutineDeclarationBody(source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := proc.EarthDeclarationCubeSchema(body, sqltype.EarthLongitude); err == nil {
			t.Fatal("invalid declaration placement accepted", source)
		}
	}
	for op := sqltype.EarthRadiusOperation; op <= sqltype.EarthGCToSec; op++ {
		body, err := earthDeclarationBody(op, "unused")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := proc.EarthDeclarationCubeSchema(body, op); err == nil {
			t.Fatal("scalar declaration constrained replay", op)
		}
	}
}
