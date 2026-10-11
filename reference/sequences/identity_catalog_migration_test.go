package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentitySerialAndNativeBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			// Retain the migrated witnesses' actual plain DB.Exec and DB.Query routes.
			identityRefExec(t, backend.DB, `CREATE TABLE serial_witness(id SERIAL)`, 0)
			identityRefExec(t, backend.DB, `INSERT INTO serial_witness DEFAULT VALUES`, 1)
			identityRefRows(t, backend.DB, `SELECT * FROM serial_witness`, [][]int64{{1}})
			identityRefExec(t, backend.DB, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY)`, 0)

			// These exclusions are native boundaries, not PostgreSQL errors.
			for _, boundary := range []struct {
				query    string
				message  string
				position string
			}{
				{`UPDATE i SET id=DEFAULT`, "UPDATE DEFAULT is unsupported", "17"},
			} {
				result, err := backend.DB.Exec(boundary.query)
				if backend.PG {
					if err != nil {
						t.Fatal(boundary.query, err)
					}
					if count, err := result.RowsAffected(); err != nil || count != 0 {
						t.Fatal(boundary.query, count, err)
					}
				} else {
					if result != nil {
						t.Fatalf("%s published a result on failure: %v", boundary.query, result)
					}
					want := fixture.ControlsReferenceDiagnostic("0A000", boundary.message)
					want.Position = boundary.position
					fixture.AssertDiagnostic(t, err, &want)
				}
			}
			// INCLUDING IDENTITY now admits independently owned sequence behavior.
			identityRefExec(t, backend.DB, `CREATE TABLE clone(LIKE i INCLUDING IDENTITY)`, 0)
			identityRefExec(t, backend.DB, `INSERT INTO clone DEFAULT VALUES`, 1)
			identityRefRows(t, backend.DB, `SELECT * FROM clone`, [][]int64{{1}})
		})
	}
}
