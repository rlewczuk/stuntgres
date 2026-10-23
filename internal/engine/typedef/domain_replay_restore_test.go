package typedef

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func replayBuiltin(t *testing.T, match func(*sqltype.Descriptor) bool) *sqltype.Descriptor {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if match(d) {
			return d
		}
	}
	t.Fatal("missing builtin type")
	return nil
}

// The scalar leaf is the engine's callback: the walk parses and shape-checks
// the captured SELECT text, hands the callback the single target and the
// leaf's usage, and rejects a NULL or mistyped result itself.
func TestRestoreDomainReplayScalarLeaf(t *testing.T) {
	int4 := replayBuiltin(t, func(d *sqltype.Descriptor) bool { return d.ID() == sqltype.Int4 })
	int4Array := replayBuiltin(t, func(d *sqltype.Descriptor) bool { return d.IsArray() && d.Element().ID() == sqltype.Int4 })
	oid := replayBuiltin(t, func(d *sqltype.Descriptor) bool { return d.ID() == sqltype.Oid })
	invalid := "stuntgres: invalid native domain replay value"
	scalar := func(text string) DomainReplayNode { return DomainReplayNode{Kind: "scalar", Scalar: &text} }
	var calls []sqltype.Usage
	leaf := func(raw parser.Expr, u sqltype.Usage) (sqltype.Value, error) {
		calls = append(calls, u)
		literal, ok := raw.(parser.LiteralExpr)
		if !ok || literal.Value.Kind != parser.IntegerValue {
			t.Fatalf("callback got %T, want the parsed integer target", raw)
		}
		return u.Type.Input(context.Background(), strconv.FormatInt(literal.Value.Integer, 10), -1)
	}
	env := &DomainReplayEnv{Context: context.Background(), Scalar: leaf}

	elements := []DomainReplayNode{scalar("7"), scalar("8")}
	dimensions := []DomainReplayDimension{{Lower: 1, Length: 2}}
	value, provenance, err := RestoreDomainReplayNode(env, int4Array.Usage(), &DomainReplayNode{Kind: "array", Dimensions: &dimensions, Elements: &elements})
	if err != nil || provenance != 0 || value.Type() != int4Array || value.ArrayLen() != 2 {
		t.Fatal(value, provenance, err)
	}
	if len(calls) != 2 || calls[0].Type != int4Array.Element() || calls[1].Type != int4Array.Element() {
		t.Fatal("leaf usages", calls)
	}

	calls = nil
	shaped := scalar("7 FROM pg_catalog.pg_class")
	if _, _, err := RestoreDomainReplayNode(env, int4.Usage(), &shaped); err == nil || err.Error() != invalid || len(calls) != 0 {
		t.Fatal("non-scalar SELECT reached the callback", err, calls)
	}

	seven := scalar("7")
	for name, result := range map[string]func(sqltype.Usage) (sqltype.Value, error){
		"null":     func(u sqltype.Usage) (sqltype.Value, error) { return sqltype.Null(u.Type), nil },
		"mistyped": func(sqltype.Usage) (sqltype.Value, error) { return sqltype.OIDValue(oid, 7) },
	} {
		wrong := &DomainReplayEnv{Context: context.Background(), Scalar: func(_ parser.Expr, u sqltype.Usage) (sqltype.Value, error) { return result(u) }}
		if _, _, err := RestoreDomainReplayNode(wrong, int4.Usage(), &seven); err == nil || err.Error() != invalid {
			t.Fatal(name, err)
		}
	}

	failure := errors.New("leaf failed")
	failing := &DomainReplayEnv{Context: context.Background(), Scalar: func(parser.Expr, sqltype.Usage) (sqltype.Value, error) { return sqltype.Value{}, failure }}
	if _, _, err := RestoreDomainReplayNode(failing, int4.Usage(), &seven); err != failure {
		t.Fatal("leaf error", err)
	}

	calls = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := &DomainReplayEnv{Context: ctx, Scalar: leaf}
	if _, _, err := RestoreDomainReplayNode(canceled, int4.Usage(), &seven); !errors.Is(err, context.Canceled) || len(calls) != 0 {
		t.Fatal("cancellation", err, calls)
	}
}
