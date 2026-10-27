package sqltype

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/util"
)

type bfCancelContext struct {
	context.Context
	calls, after int
	failure      error
}

func (c *bfCancelContext) Err() error {
	c.calls++
	if c.after != 0 && c.calls >= c.after {
		return c.failure
	}
	return nil
}

func TestPgcryptoBFCancellation(t *testing.T) {
	// Three complete64KiB chunks, plus a CFB partial tail. Baseline poll counts
	// account for entry, each data chunk and final publication independently.
	aligned := strings.Repeat("\x80\x00\xff\x01\x7f\xfe\x55\xaa", 3*65536/8)
	for _, mode := range []string{"cbc", "ecb", "cfb"} {
		data, polls := aligned, 5
		if mode == "cfb" {
			data, polls = aligned+"end", 6
		}
		for _, fn := range []string{"encrypt_iv", "decrypt_iv"} {
			typ := "bf-" + mode + "/pad:none"
			baseline := &bfCancelContext{Context: context.Background()}
			out, err := pgcryptoRaw(baseline, fn, data, "raw\x00key\xff", "iv", typ)
			if err != nil || len(out) != len(data) || baseline.calls != polls {
				t.Fatal("poll bound", fn, mode, len(out), baseline.calls, polls, err)
			}
			for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
				for _, after := range []int{1, 2, 3, polls} {
					ctx := &bfCancelContext{Context: context.Background(), after: after, failure: failure}
					out, err := pgcryptoRaw(ctx, fn, data, "raw\x00key\xff", "iv", typ)
					if out != "" || err != failure || ctx.calls != after {
						t.Fatal("canceled BF published output or wrong error", fn, mode, after, len(out), ctx.calls, err)
					}
				}
			}
		}
	}
	for _, fn := range []string{"encrypt_iv", "decrypt_iv"} {
		// Empty CFB still observes cancellation before final publication.
		ctx := &bfCancelContext{Context: context.Background(), after: 2, failure: context.DeadlineExceeded}
		out, err := pgcryptoRaw(ctx, fn, "", "k", "iv", "bf-cfb")
		if out != "" || err != context.DeadlineExceeded || ctx.calls != 2 {
			t.Fatal("empty streaming publication", fn, out, ctx.calls, err)
		}
	}
}

var pgcryptoBFAllocationSink string

func TestPgcryptoBFIsolationAndAllocations(t *testing.T) {
	initial := pgcryptoBlowfishInitial
	vectors := []struct{ plain, key, iv, typ, cipher string }{
		{"foo", "0123456", "abcd", "bf-cbc", aesUnitHex(t, "95c7e89322525d59")},
		{aesUnitHex(t, "fedcba9876543210"), aesUnitHex(t, "f0"), "ignored", "bf-ecb/pad:none", aesUnitHex(t, "f9ad597c49db005e")},
		{aesUnitHex(t, "37363534333231204e6f77206973207468652074696d6520666f722000"), aesUnitHex(t, "0123456789abcdeff0e1d2c3b4a59687"), aesUnitHex(t, "fedcba9876543210"), "bf-cfb", aesUnitHex(t, "e73214a2822139caf26ecf6d2eb9e76e3da3de04d1517200519d57a6c3")},
	}
	// Each worker alternates independent schedules/modes and checks both
	// directions against literal inputs; channel publication joins every worker.
	results := make(chan error, 8)
	for worker := range 8 {
		go func() {
			for repeat := range 8 {
				v := vectors[(worker+repeat)%len(vectors)]
				for _, fn := range []string{"encrypt_iv", "decrypt_iv"} {
					input, expected := v.plain, v.cipher
					if fn == "decrypt_iv" {
						input, expected = v.cipher, v.plain
					}
					out, err := pgcryptoRaw(context.Background(), fn, input, v.key, v.iv, v.typ)
					if err != nil || out != expected {
						results <- fmt.Errorf("worker%d %s %s got=%x want=%x err=%v", worker, fn, v.typ, out, expected, err)
						return
					}
				}
			}
			results <- nil
		}()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if pgcryptoBlowfishInitial != initial {
		t.Fatal("raw BF mutated the shared initial constants")
	}
	if t.Failed() {
		return
	}
	short, large := "12345678", strings.Repeat("12345678", 1<<17)
	key, oversized := "key", strings.Repeat("key", 1<<19)
	type allocationMeasurement struct {
		bytes, allocations float64 // Per provider evaluation, not per batch.
	}
	measure := func(fn, input, key, typ string) allocationMeasurement {
		evaluate := func() error {
			var err error
			pgcryptoBFAllocationSink, err = pgcryptoRaw(context.Background(), fn, input, key, "iv", typ)
			return err
		}
		// Match the hash-kernel proof: warmed AllocsPerRun for exact counts,
		// the smallest of three 16-evaluation samples for bytes.
		allocations := testing.AllocsPerRun(20, func() {
			if err := evaluate(); err != nil {
				t.Fatal(err)
			}
		})
		_, bytes := util.AllocationsPerCall(16, func() {
			if err := evaluate(); err != nil {
				t.Fatal(err)
			}
		})
		return allocationMeasurement{
			bytes:       float64(bytes),
			allocations: allocations,
		}
	}
	for _, typ := range []string{"bf-cbc/pad:none", "bf-cfb"} {
		for _, fn := range []string{"encrypt_iv", "decrypt_iv"} {
			a := measure(fn, short, key, typ)
			b := measure(fn, large, key, typ)
			c := measure(fn, large, oversized, typ)
			for _, v := range []struct {
				result allocationMeasurement
				size   int
			}{{a, len(short)}, {b, len(large)}, {c, len(large)}} {
				if v.result.bytes > float64(v.size+32768) || v.result.allocations > 16 {
					t.Fatal("output plus fixed scratch budget (bytes/allocations per evaluation)", fn, typ, v.size, v.result)
				}
			}
			if b.allocations-a.allocations > 2 || b.bytes-a.bytes-float64(len(large)-len(short)) > 32768 {
				t.Fatal("block-proportional allocation or redundant payload copy (per evaluation)", fn, typ, a, b)
			}
			// The same independent32KiB scratch allowance applies to key growth;
			// a1.5MiB key copy fails both this bound and the absolute budget.
			// Separate byte samples need not match exactly: their small runtime
			// noise is not input-sized allocation. Exact kernel counts still match.
			if c.bytes-b.bytes > 32768 || c.allocations != b.allocations {
				t.Fatal("key-size-proportional allocation (per evaluation)", fn, typ, b, c)
			}
		}
	}
}
