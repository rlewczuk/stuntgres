package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringAggregateProvider(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	for _, id := range []sqltype.ID{sqltype.Text, sqltype.Bytea} {
		t.Run(b.typ(id).Name(), func(t *testing.T) {
			typ := b.typ(id)
			p := stringAggregateProvider{typ.Usage()}
			value := func(s string) sqltype.Value {
				if id == sqltype.Bytea {
					v, err := sqltype.Binary(typ, s)
					if err != nil {
						t.Fatal(err)
					}
					return v
				}
				return sqltype.String(typ, s)
			}
			text := func(v sqltype.Value) string {
				var s string
				var err error
				if id == sqltype.Bytea {
					s, err = v.Binary()
				} else {
					s, err = v.Text()
				}
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			state, err := p.Init(ctx)
			if err != nil {
				t.Fatal(err)
			}
			null := sqltype.Null(typ)
			out, err := p.Finalize(ctx, state)
			if err != nil || !out.IsNull() || out.Type() != typ {
				t.Fatal(out, err)
			}
			for _, pair := range [][2]sqltype.Value{{null, value("ignored")}, {value(""), value("first")}, {value("x"), value(",")}, {value("y"), null}} {
				if err := p.Advance(ctx, state, pair[0], pair[1]); err != nil {
					t.Fatal(err)
				}
			}
			out, err = p.Finalize(ctx, state)
			if err != nil || text(out) != ",xy" {
				t.Fatal(out, err)
			}
			other, err := p.Init(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Advance(ctx, other, value(""), null); err != nil {
				t.Fatal(err)
			}
			empty, err := p.Finalize(ctx, other)
			if err != nil || empty.IsNull() || text(empty) != "" {
				t.Fatal(empty, err)
			}
			if text(out) != ",xy" {
				t.Fatal("cross-group result mutation")
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := p.Advance(canceled, state, value("z"), null); err != context.Canceled {
				t.Fatal(err)
			}
			again, err := p.Finalize(ctx, state)
			if err != nil || !sqltype.SameDatum(out, again) {
				t.Fatal("canceled transition mutated state", again, err)
			}
			if s, err := p.Init(canceled); s != nil || err != context.Canceled {
				t.Fatal(s, err)
			}
			if _, err := p.Finalize(canceled, state); err != context.Canceled {
				t.Fatal(err)
			}
			for _, invalid := range []any{nil, struct{}{}, (*stringAggregateState)(nil)} {
				if err := p.Advance(ctx, invalid, value("x"), null); err == nil {
					t.Fatal("invalid state accepted")
				}
				if _, err := p.Finalize(ctx, invalid); err == nil {
					t.Fatal("invalid finalize state accepted")
				}
			}
			if err := p.Advance(ctx, state, sqltype.Value{}, null); err == nil {
				t.Fatal("invalid datum accepted")
			}
			if err := p.Advance(ctx, state, value("x"), sqltype.Null(b.typ(sqltype.Int4))); err == nil {
				t.Fatal("foreign delimiter accepted")
			}
			if id == sqltype.Bytea {
				raw, err := p.Init(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, pair := range [][2]string{{"\x00\xff", "ignored"}, {"\x80", "\x00"}} {
					if err := p.Advance(ctx, raw, value(pair[0]), value(pair[1])); err != nil {
						t.Fatal(err)
					}
				}
				out, err := p.Finalize(ctx, raw)
				if err != nil || text(out) != "\x00\xff\x00\x80" {
					t.Fatal(out, err)
				}
			}
		})
	}
	invalid := stringAggregateProvider{b.typ(sqltype.Int4).Usage()}
	if _, err := invalid.Init(ctx); err == nil {
		t.Fatal("invalid provider descriptor accepted")
	}
}

func TestStringAggregateBindingIdentity(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	for i, tc := range []struct {
		call string
		slot int
	}{
		{"string_agg('a',',' ORDER BY 1)", 0}, {"string_agg('a',',' ORDER BY 1)", 0},
		{"string_agg('a','/' ORDER BY 1)", 1}, {"string_agg('a',',' ORDER BY 2)", 2},
		{"string_agg('a',',' ORDER BY 1 DESC)", 3}, {"string_agg('a',',' ORDER BY 1 NULLS FIRST)", 4},
		{"string_agg('a',',')", 5}, {"string_agg('a'::bytea,','::bytea ORDER BY 1)", 6},
	} {
		raw := statement(t, "SELECT "+tc.call+" FROM t").(*parser.Select).Targets[0]
		result, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(i, err)
		}
		if result.(aggregateExpression).slot != tc.slot {
			t.Fatal("incomplete aggregate identity", i, result, b.aggregates)
		}
		u := result.ResultUsage()
		if u.Typmod != -1 || !result.Properties().StageDependent {
			t.Fatal(u, result.Properties())
		}
	}
	for _, q := range []string{"SELECT string_agg($1,$2) FROM t", "SELECT string_agg($1,'x'::bytea) FROM t", "SELECT string_agg('x'::bytea,$1) FROM t"} {
		binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		binder.aggregateClause = "SELECT"
		raw := statement(t, q).(*parser.Select).Targets[0]
		result, err := binder.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(q, err)
		}
		want := sqltype.Bytea
		if q == "SELECT string_agg($1,$2) FROM t" {
			want = sqltype.Text
		}
		if result.ResultUsage().Type.ID() != want || binder.parameters[1].Type.ID() != want || !result.Properties().ParameterDependent {
			t.Fatal(q, result.ResultUsage(), binder.parameters, result.Properties())
		}
	}
}

func TestStringAggregateEvaluationAndPublication(t *testing.T) {
	for _, phase := range []string{"success", "value", "separator", "key", "late separator", "comparison", "replay", "finalize", "late finalize"} {
		t.Run(phase, func(t *testing.T) {
			qtext := "SELECT string_agg(id::text,',' ORDER BY id DESC) FROM t"
			if phase == "late finalize" {
				qtext = "SELECT string_agg(id::text,',' ORDER BY id DESC) FROM t GROUP BY v"
			}
			_, q, source := queryCoreFixture(t, qtext)
			node := q.Root.(*projectNode).child.(*aggregateNode)
			call := &node.calls[0]
			usage := call.argument.ResultUsage()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			values, separators, keys, advances, finalizes := 0, 0, 0, 0, 0
			sentinel := errors.New("late string aggregate delimiter")
			originalValue := call.argument
			call.argument = queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
				values++
				if phase == "value" {
					cancel()
				}
				if values == 2 {
					return sqltype.Null(usage.Type), nil
				}
				return originalValue.Eval(c)
			})
			call.separator = queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
				separators++
				if phase == "separator" {
					cancel()
				}
				if phase == "late separator" && separators == 4 {
					return sqltype.Value{}, sentinel
				}
				return sqltype.String(usage.Type, ","), nil
			})
			originalKey := call.order[0].expression
			call.order[0].expression = queryProbe(originalKey.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
				keys++
				if phase == "key" {
					cancel()
				}
				return originalKey.Eval(c)
			})
			if phase == "comparison" {
				call.order[0].comparison, _ = sqltype.BindOrdering(usage)
			}
			original := call.provider
			call.provider = aggregateProbeProvider{aggregateProvider: original, advance: func(ctx context.Context, state any, v, d sqltype.Value) error {
				advances++
				err := original.Advance(ctx, state, v, d)
				if phase == "replay" {
					cancel()
				}
				return err
			}, finalize: func(ctx context.Context, state any) (sqltype.Value, error) {
				finalizes++
				v, err := original.Finalize(ctx, state)
				if phase == "finalize" || phase == "late finalize" && finalizes == 2 {
					cancel()
				}
				return v, err
			}}
			rows, count, err := finalizeQuery(ctx, q.Root, source, nil)
			if phase == "success" {
				if err != nil || count != 1 || values != 4 || separators != 4 || keys != 4 || advances != 3 || finalizes != 1 {
					t.Fatal(count, err, values, separators, keys, advances, finalizes)
				}
				defer rows.Close()
				out := make([]sqltype.Value, 1)
				present, err := rows.Next(ctx, out)
				text, _ := out[0].Text()
				if err != nil || !present || text != "4,3,1" {
					t.Fatal(present, text, err)
				}
				if values != 4 || separators != 4 || keys != 4 {
					t.Fatal("Next re-evaluated arguments")
				}
				rows.Close()
			} else {
				if rows != nil || count != 0 || err == nil {
					t.Fatal("partial publication", rows, count, err)
				}
				if phase == "late separator" {
					if err != sentinel {
						t.Fatal(err)
					}
				} else if phase != "comparison" && err != context.Canceled {
					t.Fatal(err)
				}
			}
			if source.view != nil || source.anonymous != nil {
				t.Fatal("source owner retained", source)
			}
		})
	}
}

func TestStringAggregateColumnAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "CREATE TABLE secret(v TEXT,d TEXT,k INT)")
	roleExecute(t, e, op, "INSERT INTO secret VALUES('a',',',1)")
	ref := roleTableRef(t, e, "secret")
	reader, _ := e.Roles().Lookup("reader")
	column := ref
	column.Address.SubID = 1
	if err := e.SetObjectACL(context.Background(), column, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: e.roles.names["postgres"], Grantee: ACLGrantee{Role: reader.Ref}, Privileges: SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	roleExecute(t, e, op, "SELECT string_agg(v,',' ORDER BY 1) FROM secret").Rows.Close()
	for _, q := range []string{
		"SELECT string_agg(v,d) FROM secret",
		"SELECT string_agg(v,d) FROM secret WHERE FALSE",
		"SELECT string_agg(v,',' ORDER BY k) FROM secret",
		"SELECT string_agg(v,',' ORDER BY k) FROM secret WHERE FALSE",
		"SELECT string_agg(v,',') FROM secret HAVING string_agg(v,d)='a'",
		"SELECT string_agg(v,',') FROM secret ORDER BY string_agg(v,',' ORDER BY k)",
	} {
		roleError(t, e, op, q, "42501", "permission denied for table secret", "")
	}
}

func TestStringAggregateActualComparisonCancellation(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT string_agg(id::text,',' ORDER BY id DESC) FROM t")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &aggregateSortCancelContext{Context: ctx, cancel: cancel, aggregateOrdering: true}
	rows, count, err := finalizeQuery(probe, query.Root, source, nil)
	if !probe.comparisonEntered || err != context.Canceled || rows != nil || count != 0 || source.view != nil || source.anonymous != nil {
		t.Fatal("actual aggregate comparison cancellation/publication", probe.comparisonEntered, rows, count, err, source)
	}
}

func TestStringAggregateResultMetadata(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT string_agg(id::text,',' ORDER BY id),string_agg(NULL::bytea,''::bytea) FROM t")
	defer source.close()
	schema := query.Root.Schema()
	for i, want := range []sqltype.ID{sqltype.Text, sqltype.Bytea} {
		column := schema[i]
		if column.Name != "string_agg" || column.Usage.Type.ID() != want || column.Usage.Typmod != -1 || column.OriginTable != 0 || column.OriginAttribute != 0 || !column.NullabilityKnown || !column.Nullable {
			t.Fatal("aggregate result origin/type/nullability", i, column)
		}
		if want == sqltype.Bytea && column.Usage.Collation != (sqltype.CollationRef{}) {
			t.Fatal("binary result collation", column)
		}
	}
}
