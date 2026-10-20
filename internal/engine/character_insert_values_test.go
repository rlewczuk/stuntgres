package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCharacterSourceValuesCommonUsage(t *testing.T) {
	e := New()
	usage := func(id sqltype.ID) sqltype.Usage {
		d, ok := e.CatalogRead().LookupType(id)
		if !ok {
			t.Fatal("missing type", id)
		}
		return d.Usage()
	}
	for _, id := range []sqltype.ID{sqltype.Text, sqltype.BPChar, sqltype.VarChar} {
		if got := sqltype.CommonTypeCategory(usage(id).Type); got != 'S' {
			t.Errorf("type %d category=%q want S", id, got)
		}
	}
	// Symmetric implicit string edges preserve the first concrete candidate;
	// TEXT is preferred when already selected, not an unconditional replacement.
	for _, tc := range []struct {
		ids  []sqltype.ID
		want sqltype.ID
	}{
		{[]sqltype.ID{sqltype.Text, sqltype.BPChar}, sqltype.Text},
		{[]sqltype.ID{sqltype.BPChar, sqltype.Text}, sqltype.BPChar},
		{[]sqltype.ID{sqltype.Text, sqltype.VarChar}, sqltype.Text},
		{[]sqltype.ID{sqltype.VarChar, sqltype.Text}, sqltype.VarChar},
		{[]sqltype.ID{sqltype.BPChar, sqltype.VarChar}, sqltype.BPChar},
		{[]sqltype.ID{sqltype.VarChar, sqltype.BPChar}, sqltype.VarChar},
		{[]sqltype.ID{sqltype.BPChar, sqltype.BPChar}, sqltype.BPChar},
		{[]sqltype.ID{sqltype.VarChar, sqltype.VarChar}, sqltype.VarChar},
		{[]sqltype.ID{sqltype.Unknown, sqltype.BPChar}, sqltype.BPChar},
		{[]sqltype.ID{sqltype.VarChar, sqltype.Unknown}, sqltype.VarChar},
	} {
		rows := make([][]expr.Expr, len(tc.ids))
		for i, id := range tc.ids {
			u := sqltype.Usage{}
			if id != sqltype.Unknown {
				u = usage(id)
			}
			rows[i] = []expr.Expr{queryProbe(u, nil)}
		}
		got, err := analyze.CommonValuesUsage(rows, nil, 0, usage(sqltype.Text))
		if err != nil {
			t.Errorf("ids=%v: %v", tc.ids, err)
			continue
		}
		if got.Type.ID() != tc.want || got.Typmod != -1 {
			t.Errorf("ids=%v usage=%+v want=%d/-1", tc.ids, got, tc.want)
		}
	}
}

func TestCharacterSourceValuesSchemaAndTypmods(t *testing.T) {
	e := New()
	for _, tc := range []struct {
		first, second string
		id            sqltype.ID
		typmod        int32
	}{
		{"'b'::text", "'a '::varchar", sqltype.Text, -1},
		{"'a '::varchar", "'b'::text", sqltype.VarChar, -1},
		{"'b'::text", "'a '::char(2)", sqltype.Text, -1},
		{"'a '::char(2)", "'b'::text", sqltype.BPChar, -1},
		{"'b'::char(2)", "'a'::char(2)", sqltype.BPChar, 6},
		{"'b'::char(2)", "'a'::char(3)", sqltype.BPChar, -1},
		{"'b'::varchar(2)", "'a'::varchar(2)", sqltype.VarChar, 6},
		{"'b'::varchar(2)", "'a'::varchar(3)", sqltype.VarChar, -1},
		{"'b'::char(2)", "'a'::varchar(2)", sqltype.BPChar, -1},
		{"'a'::varchar(2)", "'b'::char(2)", sqltype.VarChar, -1},
		{"NULL", "'a'::char(2)", sqltype.BPChar, -1},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "INSERT INTO dst VALUES("+tc.first+"),("+tc.second+") ORDER BY 1").(*parser.Insert)
		query, err := b.BindValues(raw.Source.Values, false)
		if err != nil {
			t.Fatal(tc, err)
		}
		schema := query.Root.Schema()
		if len(schema) != 1 || schema[0].Name != "column1" || schema[0].Usage.Type.ID() != tc.id || schema[0].Usage.Typmod != tc.typmod {
			t.Fatal("source common schema", tc, schema)
		}
	}
	for _, tc := range []struct {
		first, second sqltype.ID
		message       string
	}{
		{sqltype.BPChar, sqltype.Bool, "VALUES types character and boolean cannot be matched"},
		{sqltype.Int4, sqltype.VarChar, "VALUES types integer and character varying cannot be matched"},
	} {
		a, _ := e.CatalogRead().LookupType(tc.first)
		b, _ := e.CatalogRead().LookupType(tc.second)
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		_, err := analyze.CommonValuesUsage([][]expr.Expr{{queryProbe(a.Usage(), nil)}, {queryProbe(b.Usage(), nil)}}, nil, 0, text.Usage())
		d, ok := err.(*sqlstate.Error)
		if !ok || d.Code != "42804" || d.Message != tc.message {
			t.Fatal("category boundary changed", tc, err)
		}
	}
}

func TestCharacterSourceValuesCrossIdentityTypmods(t *testing.T) {
	e := New()
	for _, tc := range []struct {
		name          string
		first, second sqltype.ID
	}{
		{"BPCHAR-first", sqltype.BPChar, sqltype.VarChar},
		{"VARCHAR-first", sqltype.VarChar, sqltype.BPChar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, _ := e.CatalogRead().LookupType(tc.first)
			second, _ := e.CatalogRead().LookupType(tc.second)
			text, _ := e.CatalogRead().LookupType(sqltype.Text)
			a, b := first.Usage(), second.Usage()
			a.Typmod, b.Typmod = 6, 6
			got, err := analyze.CommonValuesUsage([][]expr.Expr{{queryProbe(a, nil)}, {queryProbe(b, nil)}}, nil, 0, text.Usage())
			if err != nil || got != first.Usage() {
				t.Fatal("cross-identity width retained", tc, got, err)
			}
		})
	}
}

func TestCharacterInsertCastResolvedSourceDiagnostic(t *testing.T) {
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	for _, tc := range []struct {
		cast, diagnostic string
		id               sqltype.ID
	}{
		{"bpchar(2)", "character", sqltype.BPChar},
		{"char(2)", "character", sqltype.BPChar},
		{"varchar(2)", "character varying", sqltype.VarChar},
	} {
		t.Run(tc.cast, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := statement(t, "/*é🙂*/ INSERT INTO dst VALUES(CAST('a' AS "+tc.cast+"))").(*parser.Insert).Rows[0][0]
			node, err := b.Bind(raw, expressionPolicy{Context: analyze.AssignmentContext, Expected: integer.Usage(), Mode: sqltype.Assignment})
			if node == nil || err == nil || node.ResultUsage().Type.ID() != tc.id || node.ResultUsage().Typmod != 6 {
				t.Fatal("resolved source lost at assignment failure", node, err)
			}
			err = analyze.InsertValueError(e.CatalogRead(), err, catalog.Column{Name: "i", Usage: integer.Usage()}, raw, node.ResultUsage().Type)
			d, ok := err.(*sqlstate.Error)
			if !ok || d.Code != "42804" || d.Message != `column "i" is of type integer but expression is of type `+tc.diagnostic || d.Hint != "You will need to rewrite or cast the expression." || d.Position != "31" {
				t.Fatal("resolved source diagnostic", err)
			}
		})
	}
}
