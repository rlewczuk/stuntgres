package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ftsConfigurationScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(t, p, backend, conn)
		})
	}
}

func ftsConfigurationVector(query, value string, args ...any) fixture.Step {
	return fixture.Select(query, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{value}}, args...)
}

func ftsConfigurationError(query, code, message string) fixture.Step {
	return fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic(code, message))
}

// Mapping rows are ordered explicitly; generated configuration OIDs are compared
// through relationships rather than against another backend's allocator.
func ftsConfigurationMaps(name string, rows [][]any) fixture.Step {
	return fixture.Select("SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_catalog.pg_ts_config_map WHERE mapcfg=$1::regconfig ORDER BY maptokentype,mapseqno",
		[]string{"maptokentype", "mapseqno", "mapdict"}, []string{"INT4", "INT4", "INT8"}, rows, name)
}

func ftsConfigurationSimpleMaps() [][]any {
	var rows [][]any
	for _, token := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
		rows = append(rows, []any{token, int64(1), int64(3765)})
	}
	return rows
}
