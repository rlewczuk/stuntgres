package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Explicit native phase of Controls: exercise the real Dump serializer and its
// replay, not just the predicate deciding whether an ACL resembles the seed.
func slotsRawACLControls(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn) {
	t.Helper()
	var seed bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &seed); err != nil { t.Fatal(err) }
	const ownerACL = "{postgres=arwdDxtm/postgres}"
	const baselineACL = "{postgres=arwdDxtm/postgres,=r/postgres}"
	const reorderedACL = "{=r/postgres,postgres=arwdDxtm/postgres}"
	const columnACL = "{slots_control_column=r/postgres}"
	const emptyExpression = `pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)`
	// Dump's canonical regclass locators always quote both identifier parts.
	const relationSuffix = "::pg_catalog.aclitem[] WHERE oid=E'\"pg_catalog\".\"pg_replication_slots\"'::pg_catalog.regclass;\n"
	const columnSuffix = "::pg_catalog.aclitem[] WHERE attrelid=E'\"pg_catalog\".\"pg_replication_slots\"'::pg_catalog.regclass AND attname=E'slot_name'::pg_catalog.name;\n"
	for _, tc := range []struct {
		name, expression string
		raw any
		public, column, omit bool
	}{
		{"raw_absent", "NULL", nil, false, false, false},
		{"explicit_empty", emptyExpression, "{}", false, false, false},
		// Explicit ordinary table-default authority is effectively equal to
		// raw absence, but neither is the builtin's owner/PUBLIC seed.
		{"explicit_default", "E'" + ownerACL + "'", ownerACL, false, false, false},
		{"reordered_seed_rights", "E'" + reorderedACL + "'", reorderedACL, true, false, false},
		{"column_only", "E'" + ownerACL + "'", ownerACL, false, true, false},
		{"canonical_seed", "E'" + baselineACL + "'", baselineACL, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originStatusCleanup(t, admin, "RESET SESSION AUTHORIZATION")
			columnExpression := "NULL"
			var columnRaw any
			if tc.column { columnExpression, columnRaw = "E'" + columnACL + "'", columnACL }
			relationRepair := "UPDATE pg_catalog.pg_class SET relacl=" + tc.expression + relationSuffix
			columnRepair := "UPDATE pg_catalog.pg_attribute SET attacl=" + columnExpression + columnSuffix
			// EXECUTE is independently available for every authority witness.
			input := seed.String() + relationRepair + columnRepair + "UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_get_replication_slots()'::pg_catalog.regprocedure;\n"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(input)); err != nil { t.Fatal(err) }
			run := func(s fixture.Step) { slotsRun(t, p, b, admin, s) }
			check := func() {
				for _, s := range []fixture.Step{
					fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_replication_slots'::regclass", []string{"relacl"}, []string{"TEXT"}, [][]any{{tc.raw}}),
					fixture.Select("SELECT attacl::text FROM pg_attribute WHERE attrelid='pg_catalog.pg_replication_slots'::regclass AND attname='slot_name'", []string{"attacl"}, []string{"TEXT"}, [][]any{{columnRaw}}),
					fixture.Select("SELECT has_function_privilege('slots_control_other','pg_catalog.pg_get_replication_slots()','EXECUTE') AS executable", []string{"executable"}, []string{"BOOL"}, [][]any{{true}}),
				} {
					run(s)
					run(fixture.Prepared(s))
				}
				for _, role := range []string{"slots_control_actor", "slots_control_other", "slots_control_column"} {
					run(fixture.Exec("SET SESSION AUTHORIZATION " + role, 0))
					if tc.public {
						slotsEmpty(t, p, b, admin, slotsSelect)
					} else {
						slotsDenied(t, p, b, admin, slotsSelect, "view pg_replication_slots")
					}
					if role == "slots_control_column" && tc.column {
						projection := fixture.Select("SELECT slot_name FROM pg_catalog.pg_replication_slots", []string{"slot_name"}, []string{"NAME"}, nil)
						run(projection)
						run(fixture.Prepared(projection))
						slotsDenied(t, p, b, admin, "SELECT slot_name FROM pg_replication_slots ORDER BY restart_lsn", "view pg_replication_slots")
					}
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				}
				slotsEmpty(t, p, b, admin, slotsSelect)
				run(fixture.Select("SELECT n FROM slots_control_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(41)}}))
			}
			check()
			var captured bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &captured); err != nil { t.Fatal(err) }
			if strings.Contains(captured.String(), relationRepair) == tc.omit {
				t.Fatalf("actual relation ACL serialization lost raw state %s; expected repair:\n%scaptured dump:\n%s", tc.name, relationRepair, captured.String())
			}
			if tc.column && !strings.Contains(captured.String(), columnRepair) {
				t.Fatalf("actual column ACL serialization lost grant; expected repair:\n%scaptured dump:\n%s", columnRepair, captured.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(captured.Bytes())); err != nil { t.Fatal(err) }
			check()
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil { t.Fatal(err) }
			if !bytes.Equal(captured.Bytes(), redump.Bytes()) { t.Fatal("raw ACL replay changed serialized authority", tc.name) }
		})
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(seed.Bytes())); err != nil { t.Fatal(err) }
}
