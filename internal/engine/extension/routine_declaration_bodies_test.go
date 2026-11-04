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
	for op := sqltype.EarthRadiusOperation; op <= sqltype.EarthBox; op++ {
		t.Run(op.Name(), func(t *testing.T) {
			body, err := earthDeclarationBody(op, "cube schema")
			if err != nil {
				t.Fatal(err)
			}
			if body == nil {
				t.Fatal("missing released SQL-standard declaration body")
			}
			recipe, ok := proc.RoutineDeclarationRecipe(body)
			if !ok || !recipe.Return() {
				t.Fatal("lost SQL-standard RETURN declaration metadata")
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
	for _, op := range []sqltype.EarthOperation{sqltype.EarthLL, sqltype.EarthLatitude, sqltype.EarthLongitude, sqltype.EarthDistance, sqltype.EarthBox} {
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
		name  string
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
		if test.name == "wrong_arity" {
			if err == nil {
				t.Fatal("typed callable slot disagrees with its parsed arity")
			}
			continue
		}
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

func TestEarthQueryDeclarationShapes(t *testing.T) {
	for _, test := range []struct {
		op         sqltype.EarthOperation
		expression string
	}{
		{sqltype.EarthDistance, "sec_to_gc(cube_distance($1,$2))"},
		{sqltype.EarthDistance, "gc_to_sec(a.cube_distance($1,$2))"},
		{sqltype.EarthDistance, "sec_to_gc(a.cube_distance($2,$1))"},
		{sqltype.EarthDistance, "sec_to_gc(a.cube_distance($1,$3))"},
		{sqltype.EarthBox, "a.cube_enlarge($1,gc_to_sec($2),2)"},
		{sqltype.EarthBox, "a.cube_enlarge($1,sec_to_gc($2),3)"},
		{sqltype.EarthBox, "a.cube_enlarge($1,gc_to_sec($1),3)"},
		{sqltype.EarthBox, "a.cube_enlarge($1,gc_to_sec($2),3) FROM t"},
	} {
		body, err := proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Return: true, Parts: declarationText(test.expression)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := proc.EarthDeclarationCubeSchema(body, test.op); err == nil {
			t.Fatal("malformed query declaration placement accepted", test.expression)
		}
	}
	// A matching SELECT expression without RETURN declaration metadata does
	// not become an authentic query recipe merely by having the right calls.
	for _, test := range []struct {
		op     sqltype.EarthOperation
		source string
	}{
		{sqltype.EarthDistance, "SELECT sec_to_gc(a.cube_distance($1,$2))"},
		{sqltype.EarthBox, "SELECT a.cube_enlarge($1,gc_to_sec($2),3)"},
	} {
		body, err := proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Parts: declarationText(test.source)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := proc.EarthDeclarationCubeSchema(body, test.op); err == nil {
			t.Fatal("SELECT-only body acquired RETURN recipe placement", test.source)
		}
	}
}
