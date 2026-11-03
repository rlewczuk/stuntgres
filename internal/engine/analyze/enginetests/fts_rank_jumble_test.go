package enginetests

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestFTSRankStatementJumble(t *testing.T) {
	e := engine.New()
	identities := make(map[int64]uint32)
	for _, name := range []string{"ts_rank", "ts_rank_cd"} {
		base := uint32(3703)
		if name == "ts_rank_cd" {
			base = 3707
		}
		for _, form := range []struct {
			oid                  uint32
			types, first, second []string
		}{
			{base, []string{"real[]", "tsvector", "tsquery", "integer"}, []string{"'{0.1,0.2,0.4,1}'", "'cat:1'", "'cat'", "0"}, []string{"'{0.3,0.4,0.5,0.6}'", "'rat:2'", "'rat'", "32"}},
			{base + 1, []string{"real[]", "tsvector", "tsquery"}, []string{"'{0.1,0.2,0.4,1}'", "'cat:1'", "'cat'"}, []string{"'{0.3,0.4,0.5,0.6}'", "'rat:2'", "'rat'"}},
			{base + 2, []string{"tsvector", "tsquery", "integer"}, []string{"'cat:1'", "'cat'", "0"}, []string{"'rat:2'", "'rat'", "32"}},
			{base + 3, []string{"tsvector", "tsquery"}, []string{"'cat:1'", "'cat'"}, []string{"'rat:2'", "'rat'"}},
		} {
			t.Run(fmt.Sprintf("%s_%d", name, form.oid), func(t *testing.T) {
				id := ftsRankHeadlineJumbleForm(t, e, name, form.oid, form.types, form.first, form.second, false)
				if previous, exists := identities[id]; exists {
					t.Fatal("different selected rank overloads collapsed", previous, form.oid)
				}
				identities[id] = form.oid
			})
		}
	}
	ftsRankHeadlineJumbleParameters(t, e, false)
}

func TestFTSHeadlineStatementJumble(t *testing.T) {
	e := engine.New()
	identities := make(map[int64]uint32)
	for _, form := range []struct {
		oid                  uint32
		types, first, second []string
	}{
		{3743, []string{"regconfig", "text", "tsquery", "text"}, []string{"'english'", "'Cats'", "'cat'", "'HighlightAll=true'"}, []string{"'simple'", "'Dogs'", "'dog'", "'HighlightAll=false'"}},
		{3744, []string{"regconfig", "text", "tsquery"}, []string{"'english'", "'Cats'", "'cat'"}, []string{"'simple'", "'Dogs'", "'dog'"}},
		{3754, []string{"text", "tsquery", "text"}, []string{"'Cats'", "'cat'", "'HighlightAll=true'"}, []string{"'Dogs'", "'dog'", "'HighlightAll=false'"}},
		{3755, []string{"text", "tsquery"}, []string{"'Cats'", "'cat'"}, []string{"'Dogs'", "'dog'"}},
	} {
		t.Run(fmt.Sprint(form.oid), func(t *testing.T) {
			id := ftsRankHeadlineJumbleForm(t, e, "ts_headline", form.oid, form.types, form.first, form.second, true)
			if previous, exists := identities[id]; exists {
				t.Fatal("different selected headline overloads collapsed", previous, form.oid)
			}
			identities[id] = form.oid
		})
	}
	ftsRankHeadlineJumbleParameters(t, e, true)
}

// Observe actual analyzed providers, never a fabricated expression or a SQL row.
// Literal replacement locations are specified by the written argument starts;
// each actual contains exactly one literal and an explicit retained SQL cast.
func ftsRankHeadlineJumbleForm(t *testing.T, e *core.Engine, name string, oid uint32, types, first, second []string, headline bool) int64 {
	t.Helper()
	var identity int64
	for version, literals := range [][]string{first, second} {
		parts, normalizedParts := make([]string, len(types)), make([]string, len(types))
		var locations []int
		offset := len("SELECT ") + len(name) + 1
		for i, typ := range types {
			parts[i] = literals[i] + "::" + typ
			normalizedParts[i] = fmt.Sprintf("$%d::%s", i+1, typ)
			locations = append(locations, offset)
			offset += len(parts[i]) + 1
		}
		source := "SELECT " + name + "(" + strings.Join(parts, ",") + ")"
		normalized := "SELECT " + name + "(" + strings.Join(normalizedParts, ",") + ")"
		ast, binding := pgssAnalyzedBinding(t, e, source)
		project, ok := binding.Query.Root.(*plan.ProjectNode)
		if !ok || len(project.Expressions) != 1 {
			t.Fatal("missing original analyzed projection")
		}
		node := project.Expressions[0]
		if headline {
			if _, ok := node.(functions.FTSHeadline); !ok {
				t.Fatalf("missing real headline provider: %T", node)
			}
		} else {
			if _, ok := node.(functions.FTSRank); !ok {
				t.Fatalf("missing real rank provider: %T", node)
			}
		}
		info := expr.InfoOf(node)
		if _, ok := node.(expr.StatementJumbler); !ok || info.CallRoutineReference() == nil || info.CallRoutineReference().RoutineOID() != oid {
			t.Fatal("lost canonical provider or selected original routine", oid, info)
		}
		if info.Pos.Offset != 7 || info.Pos.Character != 8 {
			t.Fatal("original call source position", info.Pos)
		}
		before := analyze.JumbleStatement(ast, binding)
		if before.QueryID() == 0 || before.HighestParameter != 0 || !reflect.DeepEqual(before.Locations, locations) || len(before.Lists) != 0 || len(before.UtilityLocations) != 0 {
			t.Fatal("literal normalization provenance", before, locations)
		}
		text, raw := parser.NormalizeStatementText(source, 0, before.HighestParameter, before.Locations, before.Lists, before.UtilityLocations, parser.Options{StandardConformingStrings: true})
		if text != normalized || raw != source {
			t.Fatal("normalization changed source identity", text, normalized, raw, source)
		}
		if identity != 0 && identity != before.QueryID() {
			t.Fatal("literal payload changed selected call fingerprint")
		}
		identity = before.QueryID()
		if version == 0 {
			qualified := strings.Replace(source, "SELECT "+name+"(", "SELECT pg_catalog."+name+"(", 1) + " AS retained_result"
			if other := pgssAnalyzedJumble(t, e, qualified); other.QueryID() != identity {
				t.Fatal("qualification/output alias changed selected routine identity")
			}
		}
		composite := node.(expr.Composite)
		children := composite.Children()
		childInfo := make([]expr.Info, len(children))
		for i, child := range children {
			childInfo[i] = expr.InfoOf(child)
		}
		for _, reconstruct := range []func(expr.Info, []expr.Expr) (expr.Expr, error){composite.Rebuild, node.(expr.Substitutable).Substitute} {
			rebuilt, err := reconstruct(info, children)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expr.InfoOf(rebuilt), info) {
				t.Fatal("reconstruction changed callable/source occurrence metadata")
			}
			for i, child := range rebuilt.(expr.Composite).Children() {
				if !reflect.DeepEqual(expr.InfoOf(child), childInfo[i]) {
					t.Fatal("reconstruction changed written operand provenance", i)
				}
			}
			projectCopy := *project
			projectCopy.Expressions = []expr.Expr{rebuilt}
			queryCopy := *binding.Query
			queryCopy.Root = &projectCopy
			bindingCopy := *binding
			bindingCopy.Query = &queryCopy
			after := analyze.JumbleStatement(ast, &bindingCopy)
			if after.QueryID() != before.QueryID() || !reflect.DeepEqual(after.Locations, before.Locations) || after.HighestParameter != before.HighestParameter {
				t.Fatal("reconstruction changed canonical call or literal source spans")
			}
		}
		if !reflect.DeepEqual(expr.InfoOf(node), info) || analyze.JumbleStatement(ast, binding).QueryID() != identity {
			t.Fatal("observation/reconstruction mutated original binding")
		}
	}
	return identity
}

func ftsRankHeadlineJumbleParameters(t *testing.T, e *core.Engine, headline bool) {
	t.Helper()
	name, types, literal, literalType := "ts_rank", []string{"real[]", "tsvector", "tsquery", "integer"}, "'cat:1'", "tsvector"
	wantTypes := []sqltype.ID{1021, sqltype.TSQuery, sqltype.Int4}
	if headline {
		name, types, literal, literalType = "ts_headline", []string{"regconfig", "text", "tsquery", "text"}, "'Cats'", "text"
		wantTypes = []sqltype.ID{sqltype.Regconfig, sqltype.TSQuery, sqltype.Text}
	}
	var identity int64
	for _, numbers := range [][3]int{{4, 2, 1}, {1, 2, 4}} {
		parts := []string{fmt.Sprintf("$%d::%s", numbers[0], types[0]), literal + "::" + types[1], fmt.Sprintf("$%d::%s", numbers[1], types[2]), fmt.Sprintf("$%d::%s", numbers[2], types[3])}
		// Fill the otherwise untyped $3 slot while retaining the deliberately
		// reordered $4/$2/$1 call occurrences and literal placeholder $5.
		source := "SELECT " + name + "(" + strings.Join(parts, ",") + "),$3::integer AS parameter_anchor"
		ast, binding := pgssAnalyzedBinding(t, e, source)
		jumble := analyze.JumbleStatement(ast, binding)
		wantLocation := strings.Index(source, literal)
		if jumble.HighestParameter != 4 || !reflect.DeepEqual(jumble.Locations, []int{wantLocation}) || len(jumble.Lists) != 0 {
			t.Fatal("parameter/literal normalization provenance", jumble)
		}
		text, raw := parser.NormalizeStatementText(source, 0, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
		want := strings.Replace(source, literal+"::"+literalType, "$5::"+literalType, 1)
		if text != want || raw != source {
			t.Fatal("parameter-preserving literal numbering", text, want, raw)
		}
		type parameterSite struct {
			number, offset, character int
			typ                       sqltype.ID
		}
		var sites []parameterSite
		plan.QueryExpressions(binding.Query.Root, func(node expr.Expr) {
			plan.WalkExpression(node, func(child expr.Expr) {
				if parameter, ok := child.(plan.ParameterExpr); ok {
					pos := parameter.SourcePosition()
					sites = append(sites, parameterSite{int(parameter.Number), pos.Offset, pos.Character, parameter.ResultUsage().Type.ID()})
				}
			})
		})
		var expected []parameterSite
		for i, number := range numbers {
			offset := strings.Index(source, fmt.Sprintf("$%d", number))
			expected = append(expected, parameterSite{number, offset, offset + 1, wantTypes[i]})
		}
		anchor := strings.Index(source, "$3")
		expected = append(expected, parameterSite{3, anchor, anchor + 1, sqltype.Int4})
		if !reflect.DeepEqual(sites, expected) {
			t.Fatal("written parameter identity/type/location", sites, expected)
		}
		if identity != 0 && identity == jumble.QueryID() {
			t.Fatal("selected call fingerprint omitted ordered parameter numbers")
		}
		identity = jumble.QueryID()
	}
}
