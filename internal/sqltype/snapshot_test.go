package sqltype

import (
	"context"
	"testing"
)

func TestSnapshotCodecLossless(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	exact := InputEnvironment{ExactSnapshot: true}
	for _, tc := range []struct {
		id        ID
		modifiers []int32
		bad, good string
	}{
		{Numeric, []int32{5, 2}, "12.345", "12.34"},
		{VarChar, []int32{3}, "abcd", "abc"},
		{VarChar, []int32{3}, "abc ", "abc"},
		{VarChar, []int32{3}, "é界🙂x", "é界🙂"},
		{BPChar, []int32{3}, "ab", "ab "},
		{Time, []int32{3}, "12:34:56.123789", "12:34:56.123"},
		{Timestamp, []int32{3}, "2024-01-02 12:34:56.999789", "2024-01-02 12:34:56.123"},
		{Timestamptz, []int32{3}, "2024-01-02 12:34:56.123789+00", "2024-01-02 12:34:56.123+00"},
	} {
		t.Run(types[tc.id].Name()+"/"+tc.bad, func(t *testing.T) {
			u, _, err := types[tc.id].ResolveModifiers(tc.modifiers...)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []CoercionContext{Assignment, Explicit} {
				plan, err := PlanCoercion(u.Type.Usage(), u, mode)
				if err != nil {
					t.Fatal(err)
				}
				bad, err := u.Type.Input(ctx, tc.bad, -1)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := plan.Apply(ctx, bad, exact, OutputEnvironment{}); err == nil {
					t.Fatal("accepted lossy modifier", mode)
				}
				good, err := u.Type.Input(ctx, tc.good, -1)
				if err != nil {
					t.Fatal(err)
				}
				got, err := plan.Apply(ctx, good, exact, OutputEnvironment{})
				if err != nil || !SameDatum(got, good) {
					t.Fatal("changed exact datum", err)
				}
			}
			if _, err := u.Type.InputWithEnvironment(ctx, tc.bad, u.Typmod, exact); err == nil {
				t.Fatal("input erased modifier loss")
			}
			domain, err := Domain(56000+tc.id, "snapshot_domain", "public.snapshot_domain", u)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := domain.InputWithEnvironment(ctx, tc.bad, -1, exact); err == nil {
				t.Fatal("domain input erased base modifier loss")
			}
			if _, err := domain.InputWithEnvironment(ctx, tc.good, -1, exact); err != nil {
				t.Fatal("exact domain input", err)
			}
			array, err := Array(58000+tc.id, "snapshot_array", u.Type)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := array.InputWithEnvironment(ctx, `{"`+tc.bad+`"}`, u.Typmod, exact); err == nil {
				t.Fatal("array input erased element modifier loss")
			}
			if _, err := array.InputWithEnvironment(ctx, `{"`+tc.good+`",NULL}`, u.Typmod, exact); err != nil {
				t.Fatal("exact array input", err)
			}
		})
	}
}
