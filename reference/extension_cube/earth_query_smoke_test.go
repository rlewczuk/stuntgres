package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthQueryFeatureSmoke(t *testing.T) {
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		earthCoordinateNumbers(t, p, b, c, "SELECT earth_distance('(6378168)'::earth,'(6378168)'::earth) AS same,earth_distance('(6378168)'::earth,'(-6378168)'::earth) AS opposite", []string{"same", "opposite"}, [][]any{{float64(0), "20037605.73216"}}, []float64{0, 0})
		q := "SELECT earth_box($1::earth,$2::float8) AS b"
		earthQueryRoutes(t, p, b, c,
			cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{"(6378168)"}}, "(6378168)", float64(0)),
			cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{"(-6378168, -12756336, -12756336),(19134504, 12756336, 12756336)"}}, "(6378168)", float64(21000000)),
			cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{nil}}, nil, float64(0)),
			cubeConstructorSelect(b, q, []string{"b"}, []string{"CUBE"}, [][]any{{nil}}, "(6378168)", nil),
		)
		earthQueryCandidateSetup(t, p, b, c)
		earthQueryCandidateVerify(t, p, b, c)
		earthQueryStoredSetup(t, p, b, c)
		earthQueryStoredVerify(t, p, b, c)
		earthQueryNewDefault(t, p, b, c)
		earthQueryAtomicity(t, p, b, c, false)
		if !b.PG {
			earthQueryNativeControls(t, p, b, c, nil)
		}
	})
}
