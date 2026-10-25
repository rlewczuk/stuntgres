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
	if len(facts) != 1692 {
		t.Fatalf("released represented inventory: got %d, want 1692", len(facts))
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
