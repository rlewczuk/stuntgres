package engine

import (
	"stuntgres/internal/engine/expr"
	"testing"
)

func TestResultNullabilityEngine(t *testing.T) {
	for _, tc := range []struct {
		name string
		node expr.ResultNullability
	}{
		{"blocking", blockingInquiryExpression{kind: ordinaryBlockingInquiry}},
		{"safe_snapshot", blockingInquiryExpression{kind: safeSnapshotBlockingInquiry}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, site := range []expr.ResultSite{expr.ResultReturning, expr.ResultSelect, expr.ResultReturning} {
				if nullable, known := tc.node.ResultNullability(site); !nullable || !known {
					t.Fatalf("site %v: got (%v,%v), want (true,true)", site, nullable, known)
				}
			}
		})
	}
}
