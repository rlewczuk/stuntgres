package reference_test

import "testing"

func TestReferenceIdentitySerialAndNativeBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			// Retain the migrated witnesses' actual plain DB.Exec and DB.Query routes.
			identityRefExec(t, backend.db, `CREATE TABLE serial_witness(id SERIAL)`, 0)
			identityRefExec(t, backend.db, `INSERT INTO serial_witness DEFAULT VALUES`, 1)
			identityRefRows(t, backend.db, `SELECT * FROM serial_witness`, [][]int64{{1}})
			identityRefExec(t, backend.db, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY)`, 0)

			// These exclusions are native boundaries, not PostgreSQL errors.
			for _, boundary := range []struct {
				query    string
				message  string
				position string
			}{
				{`UPDATE i SET id=DEFAULT`, "UPDATE DEFAULT is unsupported", "17"},
			} {
				result, err := backend.db.Exec(boundary.query)
				if backend.pg {
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
					want := controlsReferenceDiagnostic("0A000", boundary.message)
					want.Position = boundary.position
					referenceAssertDiagnostic(t, err, &want)
				}
			}
			// INCLUDING IDENTITY now admits independently owned sequence behavior.
			identityRefExec(t, backend.db, `CREATE TABLE clone(LIKE i INCLUDING IDENTITY)`, 0)
			identityRefExec(t, backend.db, `INSERT INTO clone DEFAULT VALUES`, 1)
			identityRefRows(t, backend.db, `SELECT * FROM clone`, [][]int64{{1}})
		})
	}
}
