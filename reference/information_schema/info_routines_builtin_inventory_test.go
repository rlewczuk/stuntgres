package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// These test-owned facts come from REL_18_6 pg_proc.dat, pg_proc.h defaults
// (internal language, invoker security), and matched system_functions.sql
// replacements, not routine/display_inventory.go or native observations.
// Only the represented OID set was selected from BuiltinRoutineDeclarations;
// names and all display expectations were obtained from the released records.
// SQL-standard replacements store empty prosrc; quoted SQL bodies retain text.
// Sources: https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_proc.dat
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_proc.h
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/catalog/system_functions.sql
// The public fixed OID plus released name is the overload identity here; the
// existing full 82-cell provider witnesses independently cover input signatures.
// No expected fact is obtained by querying either backend.
type irBuiltinFact struct {
	oid                              uint32
	name, source, language, security string
}

func irBuiltinInventoryStep(facts []irBuiltinFact) fixture.Step {
	identities := make([]string, 0, len(facts))
	want := make([][]any, 0, len(facts))
	for _, fact := range facts {
		suffix := "_" + strconv.FormatUint(uint64(fact.oid), 10)
		prefix := []rune(fact.name)
		for len(string(prefix))+len(suffix) > 63 {
			prefix = prefix[:len(prefix)-1]
		}
		identity := string(prefix) + suffix
		identities = append(identities, "'"+strings.ReplaceAll(identity, "'", "''")+"'")
		want = append(want, []any{"pg_catalog", identity, fact.name, fact.source, fact.language, fact.security})
	}
	query := "SELECT specific_schema::text,specific_name::text,routine_name::text,routine_definition::text,external_language::text,security_type::text FROM information_schema.routines WHERE specific_schema='pg_catalog' AND specific_name IN (" + strings.Join(identities, ",") + ")"
	step := fixture.Select(query,
		[]string{"specific_schema", "specific_name", "routine_name", "routine_definition", "external_language", "security_type"},
		[]string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, want)
	step.Bag = true
	return step
}

func TestReferenceInfoRoutinesBuiltinInventory(t *testing.T) {
	facts := append([]irBuiltinFact(nil), irBuiltinFacts1...)
	facts = append(facts, irBuiltinFacts2...)
	facts = append(facts, irBuiltinFacts3...)
	facts = append(facts, irBuiltinCastFacts...)
	facts = append(facts,
		irBuiltinFact{6183, "ts_debug", "", "SQL", "INVOKER"},
		irBuiltinFact{6184, "ts_debug", "", "SQL", "INVOKER"},
		irBuiltinFact{3689, "ts_stat", "ts_stat1", "INTERNAL", "INVOKER"},
		irBuiltinFact{3690, "ts_stat", "ts_stat2", "INTERNAL", "INVOKER"},
		irBuiltinFact{1081, "format_type", "format_type", "INTERNAL", "INVOKER"},
		irBuiltinFact{1619, "pg_typeof", "pg_typeof", "INTERNAL", "INVOKER"},
		irBuiltinFact{6315, "pg_basetype", "pg_basetype", "INTERNAL", "INVOKER"},
		irBuiltinFact{2160, "text_pattern_lt", "text_pattern_lt", "INTERNAL", "INVOKER"},
		irBuiltinFact{2161, "text_pattern_le", "text_pattern_le", "INTERNAL", "INVOKER"},
		irBuiltinFact{2163, "text_pattern_ge", "text_pattern_ge", "INTERNAL", "INVOKER"},
		irBuiltinFact{2164, "text_pattern_gt", "text_pattern_gt", "INTERNAL", "INVOKER"},
		irBuiltinFact{3231, "pg_lsn_lt", "pg_lsn_lt", "INTERNAL", "INVOKER"},
		irBuiltinFact{3232, "pg_lsn_le", "pg_lsn_le", "INTERNAL", "INVOKER"},
		irBuiltinFact{3233, "pg_lsn_eq", "pg_lsn_eq", "INTERNAL", "INVOKER"},
		irBuiltinFact{3234, "pg_lsn_ge", "pg_lsn_ge", "INTERNAL", "INVOKER"},
		irBuiltinFact{3235, "pg_lsn_gt", "pg_lsn_gt", "INTERNAL", "INVOKER"},
		irBuiltinFact{3236, "pg_lsn_ne", "pg_lsn_ne", "INTERNAL", "INVOKER"},
		irBuiltinFact{3237, "pg_lsn_mi", "pg_lsn_mi", "INTERNAL", "INVOKER"},
		irBuiltinFact{3251, "pg_lsn_cmp", "pg_lsn_cmp", "INTERNAL", "INVOKER"},
		irBuiltinFact{5022, "pg_lsn_pli", "pg_lsn_pli", "INTERNAL", "INVOKER"},
		irBuiltinFact{5023, "numeric_pl_pg_lsn", "", "SQL", "INVOKER"},
		irBuiltinFact{5024, "pg_lsn_mii", "pg_lsn_mii", "INTERNAL", "INVOKER"},
		irBuiltinFact{3810, "pg_is_in_recovery", "pg_is_in_recovery", "INTERNAL", "INVOKER"},
		irBuiltinFact{3073, "pg_is_wal_replay_paused", "pg_is_wal_replay_paused", "INTERNAL", "INVOKER"},
		irBuiltinFact{1137, "pg_get_wal_replay_pause_state", "pg_get_wal_replay_pause_state", "INTERNAL", "INVOKER"},
		irBuiltinFact{3436, "pg_promote", "pg_promote", "INTERNAL", "INVOKER"},
		irBuiltinFact{3071, "pg_wal_replay_pause", "pg_wal_replay_pause", "INTERNAL", "INVOKER"},
		irBuiltinFact{3072, "pg_wal_replay_resume", "pg_wal_replay_resume", "INTERNAL", "INVOKER"},
		irBuiltinFact{3577, "pg_logical_emit_message", "pg_logical_emit_message_text", "INTERNAL", "INVOKER"},
		irBuiltinFact{3578, "pg_logical_emit_message", "pg_logical_emit_message_bytea", "INTERNAL", "INVOKER"},
		irBuiltinFact{6344, "pg_sync_replication_slots", "pg_sync_replication_slots", "INTERNAL", "INVOKER"},
		irBuiltinFact{3820, "pg_last_wal_receive_lsn", "pg_last_wal_receive_lsn", "INTERNAL", "INVOKER"},
		irBuiltinFact{3821, "pg_last_wal_replay_lsn", "pg_last_wal_replay_lsn", "INTERNAL", "INVOKER"},
		irBuiltinFact{3830, "pg_last_xact_replay_timestamp", "pg_last_xact_replay_timestamp", "INTERNAL", "INVOKER"},
		irBuiltinFact{6224, "pg_get_wal_resource_managers", "pg_get_wal_resource_managers", "INTERNAL", "INVOKER"},
		irBuiltinFact{6321, "pg_available_wal_summaries", "pg_available_wal_summaries", "INTERNAL", "INVOKER"},
		irBuiltinFact{6322, "pg_wal_summary_contents", "pg_wal_summary_contents", "INTERNAL", "INVOKER"},
		irBuiltinFact{6323, "pg_get_wal_summarizer_state", "pg_get_wal_summarizer_state", "INTERNAL", "INVOKER"},
		irBuiltinFact{3441, "pg_control_system", "pg_control_system", "INTERNAL", "INVOKER"},
		irBuiltinFact{3442, "pg_control_checkpoint", "pg_control_checkpoint", "INTERNAL", "INVOKER"},
		irBuiltinFact{3443, "pg_control_recovery", "pg_control_recovery", "INTERNAL", "INVOKER"},
		irBuiltinFact{3444, "pg_control_init", "pg_control_init", "INTERNAL", "INVOKER"},
		irBuiltinFact{3782, "pg_logical_slot_get_changes", "pg_logical_slot_get_changes", "INTERNAL", "INVOKER"},
		irBuiltinFact{3783, "pg_logical_slot_get_binary_changes", "pg_logical_slot_get_binary_changes", "INTERNAL", "INVOKER"},
		irBuiltinFact{3784, "pg_logical_slot_peek_changes", "pg_logical_slot_peek_changes", "INTERNAL", "INVOKER"},
		irBuiltinFact{3785, "pg_logical_slot_peek_binary_changes", "pg_logical_slot_peek_binary_changes", "INTERNAL", "INVOKER"},
		irBuiltinFact{1291, "suppress_redundant_updates_trigger", "suppress_redundant_updates_trigger", "INTERNAL", "INVOKER"},
		irBuiltinFact{3752, "tsvector_update_trigger", "tsvector_update_trigger_byid", "INTERNAL", "INVOKER"},
		irBuiltinFact{3753, "tsvector_update_trigger_column", "tsvector_update_trigger_bycolumn", "INTERNAL", "INVOKER"},
	)
	if len(facts) != 1772 {
		t.Fatalf("released represented inventory: got %d, want 1772", len(facts))
	}
	seen := make(map[uint32]bool, len(facts))
	for _, fact := range facts {
		if seen[fact.oid] {
			t.Fatalf("duplicate released identity %d", fact.oid)
		}
		seen[fact.oid] = true
	}
	// Each serial chunk owns a normal 30-second scenario. Batch each projection
	// rather than issuing an identity query or a routines scan for every row.
	const batch = 200
	for start := 0; start < len(facts); start += batch {
		end := min(start+batch, len(facts))
		t.Run(fmt.Sprintf("%04d-%04d", start, end-1), func(t *testing.T) {
			irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
				step := irBuiltinInventoryStep(facts[start:end])
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
			})
		})
	}
}
