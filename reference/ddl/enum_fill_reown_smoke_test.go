package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceEnumAlterFillReownFeatureSmoke is the new-behavior smoke for the
// fill-reowning publication contract: the exact replacement repro, an ADD VALUE
// variant on the relation carrying the reowned fill, and the enum-array variant,
// all through the direct route. Expected rows and types are the pinned 18.6
// captures, never native output.
func TestReferenceEnumAlterFillReownFeatureSmoke(t *testing.T) {
	fixture.EnumReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		for _, steps := range [][]fixture.Step{
			enumFillReplacement(enumFillNames("")),
			enumFillReplacementAddValue(enumFillNames("")),
			enumFillArray(enumFillNames("")),
		} {
			for _, step := range steps {
				fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, step)
			}
		}
	})
}
