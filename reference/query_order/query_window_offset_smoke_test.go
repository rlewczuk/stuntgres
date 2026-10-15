package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowOffsetFeatureSmoke(t *testing.T) {
	queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowOffsetSeed(t, p, b)
		queryWindowOffsetCore(t, p, b)
		queryWindowOffsetVector(t, p, b, "lead(v,n,id*100) OVER w", []any{nil, nil, nil, nil, int64(500), int64(600)}, "WINDOW w AS(PARTITION BY p ORDER BY id ROWS CURRENT ROW EXCLUDE CURRENT ROW)")
		queryWindowRoutes(t, p, b, fixture.Select("SELECT lead(1,1,0.5) OVER() AS n,lead(NULL,1,'fallback') OVER() AS t", []string{"n", "t"}, []string{"NUMERIC", "TEXT"}, [][]any{{"0.5", "fallback"}}))
		queryWindowValueDiagnosticRoutes(t, p, b, "SELECT lead(NULL) OVER()", fixture.Diagnostic{Code: "42804", Message: "could not determine polymorphic type because input has type unknown"}, "prepare")
		queryWindowValueDiagnosticRoutes(t, p, b, "/*é*/ SELECT lead(1)", fixture.Diagnostic{Code: "42809", Message: "window function lead requires an OVER clause", Position: "14"}, "prepare")
		queryWindowValueDiagnosticRoutes(t, p, b, "SELECT lag() OVER()", fixture.Diagnostic{Code: "42883", Message: "function lag() does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, "prepare")
		queryWindowOffsetMetadata(t, p, b)
		queryWindowOffsetHeld(t, p, b)
		queryWindowOffsetAtomic(t, p, b)
		queryWindowOffsetViews(t, p, b)
	})
}
