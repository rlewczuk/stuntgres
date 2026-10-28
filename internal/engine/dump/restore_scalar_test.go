package dump

import (
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func selectTarget(t *testing.T, sql string) parser.Expr {
	t.Helper()
	statements, err := parser.Parse(sql)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	return statements[0].(*parser.Select).Targets[0]
}

func TestRestoreScalarSubscriptModes(t *testing.T) {
	column := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "a"}}}}
	bound := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "abs"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}}}
	raw := parser.SubscriptExpr{Base: column, Subscripts: []parser.ArraySubscript{{Upper: bound}}}
	if err := RestoreScalar(raw, true); err != nil {
		t.Fatal("stored binder must retain column/function admission", err)
	}
	if err := RestoreScalar(raw, false); err == nil {
		t.Fatal("INSERT Restore whitelist expanded")
	}
	position := parser.Position{Character: 49}
	bound.Args = []parser.Expr{parser.ParameterExpr{Number: 1, Pos: position}}
	raw.Subscripts[0].Upper = bound
	err := RestoreScalar(raw, true)
	var diagnostic interface{ SQLState() string }
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "42P02" {
		t.Fatal(err)
	}
	state, ok := err.(*sqlstate.Error)
	if !ok || state.Message != "parameters are not allowed in restore" || state.Position != "49" {
		t.Fatal(err)
	}
}

func TestRestoreScalarReferenceLiteralBoundary(t *testing.T) {
	literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: `"public"."anchor"`}}
	if err := RestoreScalar(parser.RegclassExpr{Child: literal}, false); err != nil {
		t.Fatal("canonical stored reference literal rejected", err)
	}
	if err := RestoreScalar(parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, false); err != nil {
		t.Fatal("ordinary stored NULL rejected", err)
	}
	for _, tc := range []struct {
		name     string
		child    parser.Expr
		accepted bool
	}{
		{"absent", nil, false},
		{"null_cast", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, true},
		{"numeric", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 7}}, true},
		{"parameter", parser.ParameterExpr{Number: 1}, false},
		{"column", parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "n"}}}}, false},
		{"otherwise_restorable_call", parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "upper"}}}, Args: []parser.Expr{literal}}, true},
		{"effect", parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "nextval"}}}, Args: []parser.Expr{literal}}, false},
		{"query", parser.SubqueryExpr{Query: &parser.Select{Targets: []parser.Expr{literal}}}, false},
		{"nested_cast", parser.CastExpr{Child: literal, TypeName: parser.TypeName{Name: "text"}}, true},
		{"nested_reference", parser.RegclassExpr{Child: literal}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RestoreScalar(parser.RegclassExpr{Child: tc.child}, false)
			if tc.name == "parameter" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" || diagnostic.Message != "parameters are not allowed in restore" {
					t.Fatal("existing parameter rejection lost", err)
				}
				return
			}
			if tc.accepted {
				if err != nil {
					t.Fatal("selected restorable scalar child rejected", err)
				}
			} else if err == nil || err.Error() != "stuntgres: invalid restore scalar" {
				t.Fatal("unsupported scalar child admitted", err)
			}
		})
	}
}

func TestRestoreScalarCastParameterGuard(t *testing.T) {
	raw := selectTarget(t, "SELECT CAST($1::text AS int4) FROM t")
	err := RestoreScalar(raw, false)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "42P02" || d.Position != "13" {
		t.Fatal(err)
	}
	if err := RestoreScalar(selectTarget(t, "SELECT CAST('42'::text AS int4) FROM t"), false); err != nil {
		t.Fatal(err)
	}
}

func TestFTSOpsRestoreScalar(t *testing.T) {
	for _, expression := range []string{
		"tsvector_concat('a','b')", "tsquery_and('a','b')", "tsquery_or('a','b')",
		"tsquery_not('a')", "tsquery_phrase('a','b')", "tsquery_phrase('a','b',16385)",
		"tsq_mcontains('a','b')", "tsq_mcontained('a','b')",
	} {
		for _, prefix := range []string{"", "pg_catalog."} {
			if err := RestoreScalar(selectTarget(t, "SELECT "+prefix+expression), false); err != nil {
				t.Fatal("selected scalar rejected", expression, err)
			}
		}
	}
	for _, stored := range []bool{false, true} {
		err := RestoreScalar(selectTarget(t, "SELECT tsquery_phrase(NULL::tsquery,$1::tsquery,1)"), stored)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" || diagnostic.Message != "parameters are not allowed in restore" || diagnostic.Position != "37" {
			t.Fatal("strict NULL hid Restore parameter", err)
		}
	}
	if err := RestoreScalar(selectTarget(t, "SELECT to_tsquery('a')"), false); err == nil {
		t.Fatal("unlisted INSERT Restore recipe admitted")
	}
}

func TestRestoreScalarCubeConstructors(t *testing.T) {
	for _, expression := range []string{
		"cube(1::double precision)",
		"cube(1::double precision,2::double precision)",
		"cube(ARRAY[1,2]::double precision[])",
		"cube(ARRAY[1,2]::double precision[],ARRAY[3,4]::double precision[])",
		"cube('(1),(2)'::cube,3::double precision)",
		"cube('(1),(2)'::cube,3::double precision,4::double precision)",
		"cube_dim('(1,2)'::cube)",
		"cube_is_point('(1,2)'::cube)",
		"cube(cube(1::double precision),2::double precision)",
	} {
		for _, prefix := range []string{"", "cube_restore_ns."} {
			raw := selectTarget(t, "SELECT "+prefix+expression)
			for _, stored := range []bool{false, true} {
				if err := RestoreScalar(raw, stored); err != nil {
					t.Fatal("selected cube scalar recipe rejected", expression, prefix, stored, err)
				}
			}
		}
	}
	for _, name := range []string{"cube", "cube_dim", "cube_is_point"} {
		// Traversal cannot use STRICT NULL to hide a parameter. Signature
		// validation belongs to the normal binder, after this syntax guard.
		raw := parser.FunctionExpr{
			Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "cube_restore_ns"}, {Text: name}}},
			Args: []parser.Expr{
				parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}},
				parser.CastExpr{
					Child:    parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 49}},
					TypeName: parser.TypeName{Name: "double precision"},
				},
			},
		}
		for _, stored := range []bool{false, true} {
			err := RestoreScalar(raw, stored)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" || diagnostic.Message != "parameters are not allowed in restore" || diagnostic.Position != "49" {
				t.Fatal("cube recipe hid a Restore parameter", name, stored, err)
			}
		}
	}
	for _, expression := range []string{
		"cube_distance('(1)'::cube,'(2)'::cube)",
		"cube_ll_coord('(1)'::cube,1)",
		"cube_union('(1)'::cube,'(2)'::cube)",
	} {
		if err := RestoreScalar(selectTarget(t, "SELECT "+expression), false); err == nil {
			t.Fatal("adjacent cube operation admitted", expression)
		}
	}
}

func TestRangeTypeRestoreRecipeEligibility(t *testing.T) {
	eligible := func(r parser.FunctionExpr) bool {
		return len(r.Name.Parts) == 2 && r.Name.Parts[0].Text == "public" && (r.Name.Parts[1].Text == "left" || r.Name.Parts[1].Text == "right")
	}
	for _, sql := range []string{`SELECT public."left"(1,3,'[]')`, `SELECT public."right"()`, `SELECT public."right"('a','b','c')`} {
		raw := selectTarget(t, sql)
		if RestoreFunctionRecipe(raw.(parser.FunctionExpr)) {
			t.Fatal("constructor shape mistaken for incumbent static recipe", sql)
		}
		if err := RestoreScalar(raw, false, eligible); err != nil {
			t.Fatal("qualified constructor eligibility must supplement static recipe shape", sql, err)
		}
		if err := RestoreScalar(raw, false); err == nil {
			t.Fatal("constructor widened the unconfigured static whitelist", sql)
		}
	}
	incumbent := selectTarget(t, `SELECT pg_catalog.left('abc',2)`).(parser.FunctionExpr)
	if !RestoreFunctionRecipe(incumbent) {
		t.Fatal("incumbent recipe shape lost")
	}
	if err := RestoreScalar(incumbent, false, func(parser.FunctionExpr) bool { return false }); err != nil {
		t.Fatal("incumbent recipe became dependent on constructor eligibility", err)
	}
	for _, sql := range []string{`SELECT forged."right"()`, `SELECT "right"()`} {
		if err := RestoreScalar(selectTarget(t, sql), false, eligible); err == nil {
			t.Fatal("unqualified/forged namespace gained eligibility", sql)
		}
	}
	err := RestoreScalar(selectTarget(t, `SELECT public."right"(NULL,public."left"($1,3,'[]'))`), false, eligible)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" {
		t.Fatal("supplemental strict constructor skipped recursive parameter guard", err)
	}
}

func TestHstoreRecordRestoreScalar(t *testing.T) {
	for _, prefix := range []string{"", "public."} {
		raw := selectTarget(t, "SELECT "+prefix+"populate_record(NULL::pair,'i=>oops'::hstore)")
		if err := RestoreScalar(raw, false); err != nil {
			t.Fatal("record replacement must reach its ordinary selected binder", err)
		}
	}
	for _, stored := range []bool{false, true} {
		err := RestoreScalar(selectTarget(t, "SELECT populate_record(NULL::pair,$1::hstore)"), stored)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" || diagnostic.Message != "parameters are not allowed in restore" {
			t.Fatal("record recipe hid a Restore parameter", err)
		}
	}
	for _, sql := range []string{
		"SELECT populate_record(NULL::pair)",
		"SELECT populate_record(NULL::pair,''::hstore,1)",
		"SELECT populate_record(NULL::pair,VARIADIC ARRAY['i=>42'])",
		"SELECT populate_record(NULL::pair,unlisted_function())",
	} {
		if err := RestoreScalar(selectTarget(t, sql), false); err == nil {
			t.Fatal("unsupported replacement recipe/child admitted", sql)
		}
	}
}
