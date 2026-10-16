package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionCapabilities(t *testing.T) {
	partitionExclusionCase(t, "physical_gist", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pecap_root(id INT,tenant INET,during INT4RANGE,payload TEXT,CONSTRAINT pecap_ex EXCLUDE USING gist(tenant inet_ops WITH =,during WITH &&) INCLUDE(payload)) PARTITION BY RANGE(tenant)", 0))
		run(fixture.Exec("CREATE TABLE pecap_leaf PARTITION OF pecap_root DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pecap_root VALUES(1,'10.0.0.1','[1,5)','kept')", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO pecap_root VALUES(2,'10.0.0.1','[3,7)','rejected')", fixture.ExclusionReferenceConflict("pecap_leaf", "pecap_leaf_tenant_during_payload_excl", "tenant, during", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)")))
		run(fixture.Select("SELECT i.relname,i.relkind,i.relam=783 AS gist,c.contype,c.conislocal,c.coninhcount,c.connoinherit,c.condeferrable,c.condeferred,p.conname AS parent FROM pg_constraint c JOIN pg_class i ON i.oid=c.conindid JOIN pg_constraint p ON p.oid=c.conparentid WHERE c.conrelid='pecap_leaf'::regclass::OID AND c.contype='x'", []string{"relname", "relkind", "gist", "contype", "conislocal", "coninhcount", "connoinherit", "condeferrable", "condeferred", "parent"}, []string{"NAME", "CHAR", "BOOL", "CHAR", "BOOL", "INT2", "BOOL", "BOOL", "BOOL", "NAME"}, [][]any{{"pecap_leaf_tenant_during_payload_excl", "i", true, "x", false, int64(1), false, false, false, "pecap_ex"}}))

		bools := func(query string, labels []string, values []any) fixture.Step {
			types := make([]string, len(labels))
			nullable := make([]bool, len(labels))
			for i := range types {
				types[i], nullable[i] = "BOOL", true
			}
			s := fixture.Select(query, labels, types, [][]any{values})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, columns, labels, types, nullable)
				for _, column := range columns {
					if n, known := column.Length(); n != 0 || known {
						t.Fatal("BOOL length", column.Name(), n, known)
					}
					if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
						t.Fatal("BOOL decimal size", column.Name(), precision, scale, known)
					}
				}
			}
			return s
		}
		// REL_18_0 gisthandler: clusterable, tuple/bitmap callbacks, no backward scans.
		props := []string{"clusterable", "backward_scan", "index_scan", "bitmap_scan"}
		calls := make([]string, len(props))
		for i, prop := range props {
			calls[i] = "pg_index_has_property('pecap_leaf_tenant_during_payload_excl','" + prop + "') AS " + prop
		}
		run(bools("SELECT "+strings.Join(calls, ","), props, []any{true, false, true, true}))
		// gistproperty consults each key's support procedures. range_ops has no
		// compress procedure; inet_ops has a fetch procedure: both return data.
		// INCLUDE has no opclass: GiST's callback returns NULL even for returnable
		// and distance_orderable, before the generic INCLUDE fallback is reached.
		props = []string{"asc", "desc", "nulls_first", "nulls_last", "orderable", "distance_orderable", "returnable", "search_array", "search_nulls"}
		for _, tc := range []struct {
			ordinal int
			values  []any
		}{
			{1, []any{false, false, false, false, false, false, true, false, true}},
			{2, []any{false, false, false, false, false, false, true, false, true}},
			{3, []any{nil, nil, nil, nil, false, nil, nil, nil, nil}},
		} {
			calls = make([]string, len(props))
			for i, prop := range props {
				calls[i] = fmt.Sprintf("pg_index_column_has_property('pecap_leaf_tenant_during_payload_excl',%d,'%s') AS %s", tc.ordinal, prop, prop)
			}
			run(bools("SELECT "+strings.Join(calls, ","), props, tc.values))
		}
		for _, ordinal := range []int{-1, 0, 4} {
			run(bools(fmt.Sprintf("SELECT pg_index_column_has_property('pecap_leaf_tenant_during_payload_excl',%d,'returnable') AS result", ordinal), []string{"result"}, []any{nil}))
		}
		run(bools("SELECT pg_index_has_property('pecap_leaf_tenant_during_payload_excl','unknown') AS whole,pg_index_column_has_property('pecap_leaf_tenant_during_payload_excl',1,'clusterable') AS col", []string{"whole", "col"}, []any{nil, nil}))
		run(fixture.Select("SELECT id,payload FROM pecap_root ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "kept"}}))
	})
}
