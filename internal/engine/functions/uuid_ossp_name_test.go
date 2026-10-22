package functions

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestUUIDOsspNameKernel(t *testing.T) {
	ns := uuidUnitBytes(t, "6ba7b8109dad11d180b400c04fd430c8")
	// RFC9562 A.2/A.4 publish the digests BEFORE replacing version/variant.
	payload := append(append([]byte(nil), ns[:]...), "www.example.com"...)
	md := md5.Sum(payload)
	sh := sha1.Sum(payload)
	if hex.EncodeToString(md[:]) != "5df418813aed051548a72f4a814cf09e" || hex.EncodeToString(sh[:]) != "2ed6657de927468b55e12665a8aea6a22dee3e35" {
		t.Fatal("RFC pre-mask digest", md, sh)
	}
	for _, tc := range []struct {
		version    byte
		name, want string
	}{
		{3, "www.example.com", "5df418813aed351588a72f4a814cf09e"},
		{5, "www.example.com", "2ed6657de927568b95e12665a8aea6a2"},
		{3, "www.widgets.com", "3d813cbb47fb32ba91df831e1593ac29"},
		{5, "www.widgets.com", "21f7f8de80515b8986800195ef798b6a"},
		{3, "", "c87ee6744ddc3efea74edfe25da5d7b3"},
		{5, "", "4ebd020883285d698c44ec50939c0967"},
		{3, strings.Repeat("x", 8193), "b2d5dd7288a73959b7b020f229711a48"},
		{5, strings.Repeat("x", 8193), "76e95899db575a288ab936c4d8ab2dd9"},
	} {
		for range 2 {
			got, err := uuidNameHash(context.Background(), ns, tc.name, tc.version)
			if err != nil || got != uuidUnitBytes(t, tc.want) || got[6]>>4 != tc.version || got[8]>>6 != 2 {
				t.Fatal("name kernel", got, tc.want, err)
			}
		}
	}
	types := formatUnitTypes()
	uuid, text := types[sqltype.UUID], types[sqltype.Text]
	namespace, err := sqltype.UUIDValue(uuid, ns)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"uuid_generate_v3", "uuid_generate_v5"} {
		calls := 0
		args := []expr.Expr{
			uuidUnitArgument{usage: uuid.Usage(), value: namespace, calls: &calls},
			uuidUnitArgument{usage: text.Usage(), value: sqltype.String(text, "www.example.com"), calls: &calls},
		}
		n, err := NewUUIDGeneration(expr.Info{Usage: uuid.Usage()}, name, args, nil)
		if err != nil {
			t.Fatal(err)
		}
		c := &uuidOsspTimeContext{uuidOsspUnitContext: uuidOsspUnitContext{uuidUnitContext: uuidUnitContext{Context: context.Background()}}}
		value, err := n.Eval(c)
		if err != nil || value.IsNull() || value.Type() != uuid || calls != 2 || c.ready != 0 || c.entropy != 0 || c.draws != 0 {
			t.Fatal("name demanded generation state", value, err, calls, c)
		}
		for _, nulls := range [][2]bool{{true, false}, {false, true}, {true, true}} {
			values := [2]sqltype.Value{namespace, sqltype.String(text, "www.example.com")}
			if nulls[0] {
				values[0] = sqltype.Null(uuid)
			}
			if nulls[1] {
				values[1] = sqltype.Null(text)
			}
			for i := range args {
				a := args[i].(uuidUnitArgument)
				a.value = values[i]
				args[i] = a
			}
			before := calls
			value, err := n.Eval(c)
			if err != nil || !value.IsNull() || value.Type() != uuid || calls != before+2 {
				t.Fatal("strict operand demand", value, err, calls)
			}
		}
		failure := errors.New("demanded name operand")
		errorNode := n
		errorNode.Args = []expr.Expr{args[0], uuidNameFailArgument{Info: expr.Info{Usage: text.Usage()}, failure: failure}}
		if _, err := errorNode.Eval(c); !errors.Is(err, failure) {
			t.Fatal("NULL hid demanded name error", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Context = ctx
		before := calls
		if _, err := n.Eval(c); !errors.Is(err, context.Canceled) || calls != before || c.ready != 0 || c.entropy != 0 || c.draws != 0 {
			t.Fatal("canceled name demanded capabilities", err, calls, c)
		}
		if _, err := n.Reconstruct(n.Info, args[:1], nil); err == nil {
			t.Fatal("accepted arity")
		}
		if _, err := n.Reconstruct(n.Info, []expr.Expr{args[1], args[0]}, nil); err == nil {
			t.Fatal("accepted reversed types")
		}
		if _, err := n.Reconstruct(n.Info, args, types[sqltype.Timestamp]); err == nil {
			t.Fatal("accepted timestamp")
		}
		bad := n
		bad.Args = []expr.Expr{nil, args[1]}
		if err := bad.replayFault(); err == nil {
			t.Fatal("accepted malformed replay")
		}
		if n.IntrinsicVolatility() != expr.Immutable || n.PlanningFold() != expr.FoldNever {
			t.Fatal("name declaration properties")
		}
	}
	// Deterministic cancellation between chunks, without sleeps or a worker race.
	ctx := &uuidNameCancelContext{Context: context.Background(), remaining: 3}
	if _, err := uuidNameHash(ctx, ns, strings.Repeat("x", 8193), 5); !errors.Is(err, context.Canceled) {
		t.Fatal("chunk cancellation", err)
	}
}

type uuidNameCancelContext struct {
	context.Context
	remaining int
}

func (c *uuidNameCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

type uuidNameFailArgument struct {
	expr.Info
	failure error
}

func (e uuidNameFailArgument) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, e.failure
}
