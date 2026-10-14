package functions

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringAggregateProvider(t *testing.T) {
	ctx := context.Background()
	typFor := func(id sqltype.ID) *sqltype.Descriptor { d, _ := aggregateUnitType(id); return d }
	for _, id := range []sqltype.ID{sqltype.Text, sqltype.Bytea} {
		t.Run(typFor(id).Name(), func(t *testing.T) {
			typ := typFor(id)
			p := StringAggregateProvider{typ.Usage()}
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
			if err := p.Advance(ctx, state, value("x"), sqltype.Null(typFor(sqltype.Int4))); err == nil {
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
	invalid := StringAggregateProvider{typFor(sqltype.Int4).Usage()}
	if _, err := invalid.Init(ctx); err == nil {
		t.Fatal("invalid provider descriptor accepted")
	}
}
