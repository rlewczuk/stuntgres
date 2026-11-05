package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCountersMutation(t *testing.T) {
	t.Run("canonical_routes", pgssCounterMutationRoutes)
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%v", prepared), func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				pgssCounterSetup(s)
				name := fmt.Sprintf("pgss_part.pgss_counter_mutation_%v", prepared)
				s.exec("CREATE TABLE " + name + "(k integer,v text)")
				work := func(step fixture.Step) {
					step.Prepared = prepared
					s.work(step)
				}
				insert := "INSERT INTO " + name + " VALUES($1::integer,$2::text),($3::integer,$4::text)"
				s.counts(insert, 0, 0, 0)
				work(fixture.Exec(insert, 2, int64(1), "a", int64(2), strings.Repeat("long payload", 100)))
				// R(integer,text)=24+8+8+32=72, rounded separately per
				// accepted event: two records, 144 bytes, two page equivalents.
				pgssCounterCheck(s, insert, 1, 2, pgssCounterWant{dirty: 2, records: 2, bytes: 144, pgWAL: true, serial: true})
				update := "UPDATE " + name + " SET v=$1::text WHERE k=$2::integer"
				s.counts(update, 0, 0, 0)
				work(fixture.Exec(update, 1, nil, int64(1)))
				// Old72 + new40, not payload bytes and not two WAL records.
				pgssCounterCheck(s, update, 1, 1, pgssCounterWant{anySharedSplit: true, sharedRequests: 1, dirty: 1, records: 1, bytes: 112, pgShared: true, pgWAL: true, serial: true})
				s.both(fixture.Select("SELECT k,v FROM "+name+" WHERE k=1", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
				deleteSQL := "DELETE FROM " + name + " WHERE k=$1::integer RETURNING k,v"
				work(fixture.Select(deleteSQL, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}, int64(1)))
				pgssCounterCheck(s, deleteSQL, 1, 1, pgssCounterWant{anySharedSplit: true, sharedRequests: 1, dirty: 1, records: 1, bytes: 40, pgShared: true, pgWAL: true, serial: true})
				// PG18 hashes the effective alias, not the target relation OID.
				// Keep both measured modes distinct across the shared history.
				zero := fmt.Sprintf("UPDATE %s zero_image_%v SET v=$1::text WHERE k=$2::integer", name, prepared)
				s.counts(zero, 0, 0, 0)
				work(fixture.Exec(zero, 0, "not accepted", int64(99)))
				pgssCounterCheck(s, zero, 1, 0, pgssCounterWant{anySharedSplit: true, sharedRequests: 1, pgShared: true, pgNoWAL: true, serial: true})
				s.both(fixture.Select("SELECT k,v FROM "+name, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), strings.Repeat("long payload", 100)}}))
			})
		})
	}
	t.Run("slot_shape_not_text_length", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			s.exec("CREATE TABLE pgss_part.pgss_counter_shape(k integer,v text)")
			q := "INSERT INTO pgss_part.pgss_counter_shape VALUES($1::integer,$2::text)"
			for i, value := range []any{"x", strings.Repeat("x", 4096), nil} {
				s.work(fixture.Prepared(fixture.Exec(q, 1, int64(i), value)))
				bytes := int64(72 * (i + 1))
				if i == 2 {
					bytes = 184 //72+72+40; NULL eliminates only the text slot.
				}
				pgssCounterCheck(s, q, int64(i+1), int64(i+1), pgssCounterWant{dirty: int64(i + 1), records: int64(i + 1), bytes: bytes, pgWAL: true, serial: true})
			}
			s.both(fixture.Select("SELECT k,length(v) AS length FROM pgss_part.pgss_counter_shape ORDER BY k", []string{"k", "length"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(1)}, {int64(1), int64(4096)}, {int64(2), nil}}))
		})
	})
	for _, persistence := range []string{"TEMP", "UNLOGGED"} {
		t.Run(persistence, func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				pgssCounterSetup(s)
				name := "pgss_counter_" + strings.ToLower(persistence)
				if persistence != "TEMP" {
					name = "pgss_part." + name
				}
				s.work(fixture.Exec("CREATE "+persistence+" TABLE "+name+"(k integer,v text)", 0))
				q := "INSERT INTO " + name + " VALUES($1::integer,$2::text),($3::integer,$4::text)"
				s.work(fixture.Prepared(fixture.Exec(q, 2, int64(1), "a", int64(2), "b")))
				want := pgssCounterWant{dirty: 2, pgNoWAL: true, serial: true}
				if persistence == "TEMP" {
					want.dirty, want.localDirty = 0, 2
				}
				pgssCounterCheck(s, q, 1, 2, want)
				read := "SELECT k,v FROM " + name + " ORDER BY k"
				s.work(fixture.Select(read, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}}))
				readWant := pgssCounterWant{read: 1, pgShared: true, pgNoWAL: true, serial: true}
				if persistence == "TEMP" {
					readWant = pgssCounterWant{localHit: 1, pgLocal: true, pgNoWAL: true, serial: true}
				}
				pgssCounterCheck(s, read, 1, 2, readWant)
			})
		})
	}
	t.Run("accepted_conflict_and_trigger_images", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			s.exec("CREATE TABLE pgss_part.pgss_counter_conflict(k integer PRIMARY KEY,v text)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_conflict VALUES(1,'old')", 1))
			q := "INSERT INTO pgss_part.pgss_counter_conflict VALUES($1::integer,$2::text) ON CONFLICT DO NOTHING"
			s.work(fixture.Prepared(fixture.Exec(q, 0, int64(1), "excluded")))
			// Conflict probes are not fabricated accepted mutation images.
			pgssCounterCheck(s, q, 1, 0, pgssCounterWant{serial: true})
			s.exec("CREATE TABLE pgss_part.pgss_counter_trigger(k integer,v text)")
			s.exec("CREATE TABLE pgss_part.pgss_counter_audit(k integer,v text)")
			s.exec("CREATE FUNCTION pgss_part.pgss_counter_trigger_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO pgss_part.pgss_counter_audit VALUES(NEW.k,NEW.v); RETURN NEW; END$$")
			s.exec("CREATE TRIGGER pgss_counter_after AFTER INSERT ON pgss_part.pgss_counter_trigger FOR EACH ROW EXECUTE FUNCTION pgss_part.pgss_counter_trigger_fn()")
			s.work(fixture.Exec("SET pg_stat_statements.track='all'", 0))
			q = "INSERT INTO pgss_part.pgss_counter_trigger VALUES($1::integer,$2::text) RETURNING k,v"
			s.work(fixture.Select(q, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "audit"}}, int64(7), "audit"))
			pgssCounterCheck(s, q, 1, 1, pgssCounterWant{dirty: 2, records: 2, bytes: 144, pgWAL: true, serial: true})
			pgssCounterCheckTop(s, "INSERT INTO pgss_part.pgss_counter_audit VALUES%", false, 1, 1, pgssCounterWant{dirty: 1, records: 1, bytes: 72, pgWAL: true, serial: true})
			s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_counter_audit", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "audit"}}))
		})
	})
}
