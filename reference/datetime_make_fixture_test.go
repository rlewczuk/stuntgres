package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Independent release-source expectations were checked against pinned PostgreSQL
// 18.0 by Main; discovery outcomes are retained in the shared oracle cases.
func datetimeMakeScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle TO 'ISO, MDY'", "SET IntervalStyle TO 'postgres'"} {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

type datetimeMakeCase struct{ name, expression, want string }

func datetimeMakeTextCases(t *testing.T, cases []datetimeMakeCase) {
	t.Helper()
	datetimeMakeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
						expression, typ := "("+tc.expression+")::text", "TEXT"
						// INTERVAL uses its existing public output codec, not an unsupported cast edge.
						if strings.HasPrefix(tc.expression, "make_interval(") {
							expression, typ = tc.expression, "INTERVAL"
						}
						s := referenceSelect("SELECT "+expression+" AS result", []string{"result"}, []string{typ}, [][]any{{tc.want}})
						s.prepared = prepared
						referenceRunStep(t, p.ctx, c, b.recorder, s)
					})
				}
			})
		}
	})
}

func datetimeMakeError(code, message string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

type datetimeMakeFailure struct {
	name, expression, code, message, hint string
	position                              int
	analysis                              bool
}

func datetimeMakeFailures(t *testing.T, cases []datetimeMakeFailure) {
	t.Helper()
	datetimeMakeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
						d := datetimeMakeError(tc.code, tc.message)
						d.Hint = tc.hint
						d.Position = referencePosition(tc.position)
						s := referenceErrorDiagnostic("SELECT "+tc.expression, d)
						s.selectRows = true
						s.prepared = prepared
						s.errorStage = "execute"
						if prepared && tc.analysis {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, c, b.recorder, s)
					})
				}
			})
		}
	})
}
