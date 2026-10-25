package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGSequenceDefinitions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgs_definition_owner"})
	fixture.SchemaReferenceCleanup(t, "pgs_definition_schema")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, tc := range []struct {
				name, ddl string
				values    []any
			}{
				{"pgs_default", "", []any{int64(20), int64(1), int64(1), int64(9223372036854775807), int64(1), int64(1), false}},
				{"pgs_descending", " AS SMALLINT INCREMENT -3", []any{int64(21), int64(-1), int64(-3), int64(-1), int64(-32768), int64(1), false}},
				{"pgs_explicit", " AS INTEGER INCREMENT 7 MINVALUE -20 MAXVALUE 200 START 15 CACHE 9 CYCLE", []any{int64(23), int64(15), int64(7), int64(200), int64(-20), int64(9), true}},
				{"pgs_huge", " CACHE 9223372036854775807", []any{int64(20), int64(1), int64(1), int64(9223372036854775807), int64(1), int64(9223372036854775807), false}},
			} {
				pgSequenceExec(t, p, b, b.DB, "CREATE SEQUENCE "+tc.name+tc.ddl)
				oid := fixture.PGDescriptionRelationID(t, p, b, tc.name)
				pgSequenceCheck(t, p, b, b.DB, oid, append([]any{oid}, tc.values...))
			}
			for _, tc := range []struct {
				name, ddl     string
				before, after []any
			}{
				{"pgs_as_default", " AS SMALLINT", []any{int64(21), int64(1), int64(1), int64(32767), int64(1), int64(1), false}, []any{int64(23), int64(1), int64(1), int64(2147483647), int64(1), int64(1), false}},
				{"pgs_as_explicit", " AS SMALLINT MAXVALUE 10", []any{int64(21), int64(1), int64(1), int64(10), int64(1), int64(1), false}, []any{int64(23), int64(1), int64(1), int64(10), int64(1), int64(1), false}},
				{"pgs_as_desc", " AS SMALLINT INCREMENT -3", []any{int64(21), int64(-1), int64(-3), int64(-1), int64(-32768), int64(1), false}, []any{int64(23), int64(-1), int64(-3), int64(-1), int64(-2147483648), int64(1), false}},
			} {
				pgSequenceExec(t, p, b, b.DB, "CREATE SEQUENCE "+tc.name+tc.ddl)
				oid := fixture.PGDescriptionRelationID(t, p, b, tc.name)
				pgSequenceCheck(t, p, b, b.DB, oid, append([]any{oid}, tc.before...))
				pgSequenceExec(t, p, b, b.DB, "ALTER SEQUENCE "+tc.name+" AS INTEGER")
				pgSequenceCheck(t, p, b, b.DB, oid, append([]any{oid}, tc.after...))
			}
			oid := fixture.PGDescriptionRelationID(t, p, b, "pgs_explicit")
			row := pgSequenceCustom(oid)
			for _, tc := range []struct {
				clause string
				index  int
				value  any
			}{
				{"START 21", 2, int64(21)}, {"INCREMENT 3", 3, int64(3)}, {"MAXVALUE 300", 4, int64(300)}, {"MINVALUE -30", 5, int64(-30)}, {"CACHE 4", 6, int64(4)}, {"NO CYCLE", 7, false}, {"AS BIGINT", 1, int64(20)},
			} {
				pgSequenceExec(t, p, b, b.DB, "ALTER SEQUENCE pgs_explicit "+tc.clause)
				row[tc.index] = tc.value
				pgSequenceCheck(t, p, b, b.DB, oid, row)
			}
			for _, q := range []string{"CREATE ROLE pgs_definition_owner", "CREATE SCHEMA pgs_definition_schema", "ALTER SEQUENCE pgs_explicit RENAME TO pgs_renamed", "ALTER SEQUENCE pgs_renamed SET SCHEMA pgs_definition_schema", "ALTER SEQUENCE pgs_definition_schema.pgs_renamed OWNER TO pgs_definition_owner"} {
				pgSequenceExec(t, p, b, b.DB, q)
				pgSequenceCheck(t, p, b, b.DB, oid, row)
			}
			if fresh := fixture.PGDescriptionRelationID(t, p, b, "pgs_renamed"); fresh != oid {
				t.Fatal("ALTER changed identity", fresh, oid)
			}
			pgSequenceExec(t, p, b, b.DB, "DROP SEQUENCE pgs_definition_schema.pgs_renamed")
			pgSequenceCheck(t, p, b, b.DB, oid, nil)
			pgSequenceExec(t, p, b, b.DB, "CREATE SEQUENCE pgs_definition_schema.pgs_renamed")
			fresh := fixture.PGDescriptionRelationID(t, p, b, "pgs_renamed")
			if fresh == oid {
				t.Fatal("recreation reused identity")
			}
			pgSequenceCheck(t, p, b, b.DB, fresh, pgSequenceDefault(fresh))
			pgSequenceExec(t, p, b, b.DB, "CREATE TABLE pgs_owned(serial_id SERIAL, identity_id BIGINT GENERATED ALWAYS AS IDENTITY)")
			for _, tc := range []struct {
				name     string
				typ, max int64
			}{{"pgs_owned_serial_id_seq", 23, 2147483647}, {"pgs_owned_identity_id_seq", 20, 9223372036854775807}} {
				id := fixture.PGDescriptionRelationID(t, p, b, tc.name)
				r := pgSequenceDefault(id)
				r[1], r[4] = tc.typ, tc.max
				pgSequenceCheck(t, p, b, b.DB, id, r)
			}
		})
	}
}

func TestReferencePGSequenceDefinitionNotState(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			pgSequenceExec(t, p, b, c, "CREATE SEQUENCE pgs_state AS INTEGER START 5 RESTART 9 CACHE 3")
			oid := fixture.PGDescriptionRelationID(t, p, b, "pgs_state")
			row := pgSequenceDefault(oid)
			row[1], row[2], row[4], row[6] = int64(23), int64(5), int64(2147483647), int64(3)
			pgSequenceCheck(t, p, b, c, oid, row)
			for _, tc := range []struct {
				q string
				n int64
			}{{"SELECT nextval('pgs_state')", 9}, {"SELECT nextval('pgs_state')", 10}, {"SELECT currval('pgs_state')", 10}, {"SELECT setval('pgs_state',42,false)", 42}, {"SELECT nextval('pgs_state')", 42}} {
				fixture.SequenceProbeValue(t, p.Ctx, c, tc.q, tc.n)
				pgSequenceCheck(t, p, b, c, oid, row)
			}
			pgSequenceExec(t, p, b, c, "ALTER SEQUENCE pgs_state RESTART WITH 17")
			pgSequenceCheck(t, p, b, c, oid, row)
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('pgs_state')", 17)
			pgSequenceExec(t, p, b, c, "ALTER SEQUENCE pgs_state START 21")
			row[2] = int64(21)
			pgSequenceCheck(t, p, b, c, oid, row)
			pgSequenceExec(t, p, b, c, "ALTER SEQUENCE pgs_state RESTART")
			pgSequenceCheck(t, p, b, c, oid, row)
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('pgs_state')", 21)
			pgSequenceCheck(t, p, b, c, oid, row)
		})
	}
}
