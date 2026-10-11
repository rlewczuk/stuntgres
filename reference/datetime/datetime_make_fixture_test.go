package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Independent release-source expectations were checked against pinned PostgreSQL
// 18.0 by Main; discovery outcomes are retained in the shared oracle cases.
func datetimeMakeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle TO 'ISO, MDY'", "SET IntervalStyle TO 'postgres'"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

type datetimeMakeCase struct{ name, expression, want string }

func datetimeMakeTextCases(t *testing.T, cases []datetimeMakeCase) {
	t.Helper()
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
						expression, typ := "("+tc.expression+")::text", "TEXT"
						// INTERVAL uses its existing public output codec, not an unsupported cast edge.
						if strings.HasPrefix(tc.expression, "make_interval(") {
							expression, typ = tc.expression, "INTERVAL"
						}
						s := fixture.Select("SELECT "+expression+" AS result", []string{"result"}, []string{typ}, [][]any{{tc.want}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					})
				}
			})
		}
	})
}

func datetimeMakeError(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

type datetimeMakeFailure struct {
	name, expression, code, message, hint string
	position                              int
	analysis                              bool
}

func datetimeMakeFailures(t *testing.T, cases []datetimeMakeFailure) {
	t.Helper()
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
						d := datetimeMakeError(tc.code, tc.message)
						d.Hint = tc.hint
						d.Position = fixture.Position(tc.position)
						s := fixture.ErrorDiagnostic("SELECT "+tc.expression, d)
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared && tc.analysis {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					})
				}
			})
		}
	})
}
