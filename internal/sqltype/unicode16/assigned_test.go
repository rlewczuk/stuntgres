package unicode16

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAssignedBoundaries(t *testing.T) {
	// Independent Unicode16 endpoints/interiors, not expectations generated from
	// assignedRanges itself. Includes large UnicodeData First/Last records.
	for _, tc := range []struct {
		code rune
		want bool
	}{
		{0x0001, true}, {0x0041, true}, {0x0301, true}, {0x0378, false}, {0x0377, true}, {0x0379, false}, {0x037A, true},
		{0x4E00, true}, {0x5ABC, true}, {0x9FFF, true}, {0xA000, true},
		{0xAC00, true}, {0xB123, true}, {0xD7A3, true}, {0xD7A4, false},
		{0xE000, true}, {0xE123, true}, {0xF8FF, true}, {0xFDD0, false}, {0xFFFE, false}, {0xFFFF, false},
		{0x20000, true}, {0x23456, true}, {0x2A6DF, true}, {0x2A6E0, false},
		{0xF0000, true}, {0xF0123, true}, {0xFFFFD, true}, {0xFFFFE, false},
		{0x100000, true}, {0x100123, true}, {0x10FFFD, true}, {0x10FFFE, false}, {0x10FFFF, false}, {0x1FAE9, true},
	} {
		got, err := Assigned(context.Background(), string(tc.code))
		if err != nil || got != tc.want {
			t.Fatalf("U+%04X: %v %v want%v", tc.code, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		s    string
		want bool
	}{{"", true}, {"A\u0378Z", false}, {"\x01\u0301\ue123", true}} {
		got, err := Assigned(context.Background(), tc.s)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for i, r := range assignedRanges {
		if r.lo > r.hi || r.lo < 0 || r.hi > 0x10ffff || i > 0 && assignedRanges[i-1].hi+1 >= r.lo {
			t.Fatal("noncanonical generated ranges", i, r)
		}
	}
}

func TestAssignedCancellationAndIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := Assigned(ctx, ""); got || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
	probe := &cancellationProbe{Context: context.Background(), remaining: 3}
	if got, err := Assigned(probe, strings.Repeat("A", 8192)); got || !errors.Is(err, context.Canceled) {
		t.Fatal("bounded membership scan", got, err)
	}
	source := strings.Repeat("A\ue123", 4096)
	if n := testing.AllocsPerRun(10, func() {
		yes, err := Assigned(context.Background(), source)
		if err != nil || !yes {
			panic("assigned identity")
		}
	}); n != 0 {
		t.Fatal("membership allocated", n)
	}
}
