package extension

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
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
	coord := func(spelling string, args ...declarationParts) declarationParts {
		return declarationCall(proc.DeclarationMember("cube", 26, uint32(ids.ProcedureClassID), spelling, 0, 23), args...)
	}
	first, second := declarationArg(0), declarationText("2")
	one := declarationText("1")
	longitude := func(left, right declarationParts) declarationParts {
		return declarationBuiltin("degrees", "701", declarationBuiltin("atan2", "701,701", left, right))
	}
	valid := longitude(coord("a.cube_ll_coord", first, second), coord("a.cube_ll_coord", first, one))
	for _, test := range []struct {
		name string
		parts declarationParts
	}{
		{"mixed_schemas", longitude(coord("a.cube_ll_coord", first, second), coord("b.cube_ll_coord", first, one))},
		{"unqualified", longitude(coord("cube_ll_coord", first, second), coord("cube_ll_coord", first, one))},
		{"wrong_arity", longitude(coord("a.cube_ll_coord", first), coord("a.cube_ll_coord", first, one))},
		{"foreign_member", longitude(coord("a.cube_ll_coord", first, second), coord("a.other", first, one))},
		{"wrong_outer_shape", coord("a.cube_ll_coord", first, one)},
		{"unexpected_from", declarationJoin(valid, declarationText(" FROM "), declarationParts{proc.DeclarationTable(0, "t")})},
	} {
		body, err := proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Return: true, Parts: test.parts})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := proc.EarthDeclarationCubeSchema(body, sqltype.EarthLongitude); err == nil {
			t.Fatal("invalid declaration placement accepted", test.name)
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
