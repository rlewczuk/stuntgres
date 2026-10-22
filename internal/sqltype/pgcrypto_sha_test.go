package sqltype

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestPgcryptoSHAVectors(t *testing.T) {
	// Independent REL_18_6/Drepper vectors plus pinned18.6 UTF8/C boundaries.
	for _, v := range []struct{ password, setting, want string }{
		{"Hello world!", "$5$saltstring", "$5$saltstring$5B8vYYiY.CVt1RlTTf8KbXBH3hsxY/GNooZaBBGWEc5"},
		{"Hello world!", "$6$saltstring", "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"Hello world!", "$5$rounds=10000$saltstringsaltstring", "$5$rounds=10000$saltstringsaltst$3xv.VbSHBb41AL9AvLeujZkZRBAwqFMz2.opqey6IcA"},
		{"Hello world!", "$6$rounds=10000$saltstringsaltstring", "$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sbHbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v."},
		{"", "$5$Szzz0yzz", "$5$Szzz0yzz$cA.ZFZKqblRYjdsbrWtVTYa/qSwPQnt2uh0LBtyYAAD"},
		{"", "$6$Szzz0yzz", "$6$Szzz0yzz$EGj.JLAovFyAtCJx3YD1DXD1yTXoO9gv4qgLyHBsJJ1lkpnLB8ZPHekm1qXjJCOBc/8thCuHpxNN8Y5xzRYU5."},
		{"Hello world!", "$5$$salt", "$5$salt$E/ML6se8AJykqQpAjfpDBzcwOvbwtQf/ixq.pr8ytQ2"},
		{"Hello world!", "$6$$$salt", "$6$salt$mRXDTpSp7pvLR./7VYKLbBWgtS1/2rgEdQgv1SY4PYQPVSCUOGN.in8mvQ5CgZ9pjsprXeaOe1d9GA4ObHYmN/"},
		{"é界😀", "$5$rounds=1000$salt", "$5$rounds=1000$salt$gK1JmQIdXq4Y1G6/fUK99rKCbrRRmCi6RIEhQ5pS6K/"},
		{strings.Repeat("a", 73), "$6$rounds=1000$salt", "$6$rounds=1000$salt$VE/IXC3hrx4goU2MuAdizpqvzRTckySt86yM3MpQdaXAUY08tW9IwgpX5l8qq7IUuh6Xko9q423EP7WISHhLo/"},
	} {
		got, err := pgcryptoCrypt(context.Background(), nil, v.password, v.setting)
		if err != nil || got != v.want {
			t.Fatalf("setting=%q password=%q got=%q want=%q err=%v", v.setting, v.password, got, v.want, err)
		}
	}
}

func TestPgcryptoSHAParsing(t *testing.T) {
	for _, id := range []string{"5", "6"} {
		for _, v := range []struct {
			suffix           string
			rounds           uint32
			custom           bool
			salt, raw, clamp string
		}{
			{"salt", 5000, false, "salt", "salt", ""},
			{"rounds=5000$salt", 5000, true, "salt", "salt", ""},
			{"rounds=1000$salt", 1000, true, "salt", "salt", ""},
			{"rounds=999999999$salt", 999999999, true, "salt", "salt", ""},
			{"rounds=+01000$salt", 1000, true, "salt", "salt", ""},
			{"rounds= \t1000$salt", 1000, true, "salt", "salt", ""},
			{"rounds=$salt", 1000, true, "salt", "salt", "0"},
			{"rounds=-1$salt", 1000, true, "salt", "salt", "-1"},
			{"rounds=2147483648$salt", 1000, true, "salt", "salt", "-2147483648"},
			{"rounds=4294968296$salt", 1000, true, "salt", "salt", ""},
			{"rounds=9223372036854775808$salt", 1000, true, "salt", "salt", "-1"},
			{"rounds=-9223372036854775809$salt", 1000, true, "salt", "salt", "0"},
			{"", 5000, false, "", "", ""},
			{"$$salt", 5000, false, "salt", "$$sa", ""},
			{"1234567890123456!", 5000, false, "1234567890123456", "1234567890123456", ""},
			{"salt$ordinary", 5000, false, "salt", "salt", ""},
		} {
			var notices []sqlstate.Error
			p, err := pgcryptoSHAParse("$"+id+"$"+v.suffix, func(n sqlstate.Error) { notices = append(notices, n) })
			if err != nil || p.id != id[0] || p.rounds != v.rounds || p.custom != v.custom || string(p.salt[:p.size]) != v.salt || p.raw != v.raw {
				t.Fatal(v, p, err)
			}
			var want []sqlstate.Error
			if v.clamp != "" {
				want = []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "22003", Message: "rounds=" + v.clamp + " is below supported value (1000), using 1000 instead"}}
			}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal(v, notices, want)
			}
		}
		for _, suffix := range []string{"rounds=x$salt", "rounds=1000", "rounds=1000x$salt", "rounds=+$salt", "rounds= $salt"} {
			_, err := pgcryptoSHAParse("$"+id+"$"+suffix, nil)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "42601" || d.Message != "could not parse salt options" {
				t.Fatal(suffix, err)
			}
		}
		var notices []sqlstate.Error
		_, err := pgcryptoSHAParse("$"+id+"$rounds=1000000000$!", func(n sqlstate.Error) { notices = append(notices, n) })
		want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "22003", Message: "rounds=1000000000 exceeds maximum supported value (999999999), using 999999999 instead"}
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22023" || d.Message != `invalid character in salt string: "!"` || !reflect.DeepEqual(notices, []sqlstate.Error{want}) {
			t.Fatal(err, notices)
		}
	}
}

type pgcryptoSHACancelContext struct {
	context.Context
	calls, after int
	failure      error
}

func (c *pgcryptoSHACancelContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return c.failure
	}
	return nil
}

func TestPgcryptoSHACancellation(t *testing.T) {
	_, text, _ := pgcryptoSaltUnitPlan(t, false)
	plan, err := BindPgcrypto("crypt", text.Usage(), text.Usage(), text.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, id := range []string{"5", "6"} {
			for _, v := range []struct {
				password, rounds string
				after            int
			}{
				{"p", "1000", 1},
				// Initial digest work is bounded well below1000 Err calls;
				//4096 password repetitions in DP exceed it, before rounds begin.
				{strings.Repeat("p", 4096), "1000", 1000},
				// With one password byte, initialization/DS need fewer than1000
				//checks;2000 therefore interrupts the admitted round loop.
				{"p", "999999999", 2000},
			} {
				ctx := &pgcryptoSHACancelContext{Context: context.Background(), after: v.after, failure: failure}
				got, err := plan.Evaluate(ctx, nil, String(text, v.password), String(text, "$"+id+"$rounds="+v.rounds+"$salt"))
				if got.Type() != nil || !errors.Is(err, failure) || ctx.calls != v.after {
					t.Fatal(id, v.after, got, err, ctx.calls)
				}
			}
		}
	}
}

func TestPgcryptoSHAIsolation(t *testing.T) {
	_, text, _ := pgcryptoSaltUnitPlan(t, false)
	plan, err := BindPgcrypto("crypt", text.Usage(), text.Usage(), text.Usage())
	if err != nil {
		t.Fatal(err)
	}
	before := plan
	for range 2 {
		for _, id := range []string{"5", "6"} {
			var notices []sqlstate.Error
			_, err := plan.Evaluate(context.Background(), func(n sqlstate.Error) { notices = append(notices, n) }, String(text, "p"), String(text, "$"+id+"$rounds=10$!"))
			if err == nil || len(notices) != 1 {
				t.Fatal(err, notices)
			}
			got, err := plan.Evaluate(context.Background(), func(n sqlstate.Error) { t.Errorf("stale notice: %+v", n) }, Null(text), String(text, "$"+id+"$rounds=10$!"))
			if err != nil || !got.IsNull() || got.Type() != text || plan != before {
				t.Fatal(got, err, plan)
			}
		}
	}
	// One immutable signature plan can evaluate different algorithms and sinks
	// concurrently. Collect every worker outcome before reporting failures.
	results := make(chan error, 8)
	for i := range 8 {
		go func() {
			setting, want := "$5$saltstring", "$5$saltstring$5B8vYYiY.CVt1RlTTf8KbXBH3hsxY/GNooZaBBGWEc5"
			if i&1 != 0 {
				setting, want = "$6$saltstring", "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"
			}
			notices := 0
			value, err := plan.Evaluate(context.Background(), func(sqlstate.Error) { notices++ }, String(text, "Hello world!"), String(text, setting))
			if err != nil {
				results <- err
				return
			}
			got, err := value.Text()
			if err != nil || got != want || notices != 0 {
				results <- fmt.Errorf("setting=%q hash=%q want=%q notices=%d err=%v", setting, got, want, notices, err)
				return
			}
			results <- nil
		}()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if plan != before {
		t.Fatal("SHA evaluation mutated signature plan")
	}
}
