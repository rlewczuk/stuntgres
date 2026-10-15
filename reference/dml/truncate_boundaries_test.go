package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateBoundaries(t *testing.T) {
	t.Run("PhysicalSystemCatalogAbsentNative", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			// PostgreSQL has protected physical system tables; native pg_class is a
			// virtual catalog authority, not invented storage with a fake truncate hook.
			expected := truncateWant("42809", `"pg_class" is not a table`)
			if b.PG {
				expected = truncateWant("42501", `permission denied: "pg_class" is a system catalog`)
			}
			truncateError(t, f, f.A, "TRUNCATE pg_catalog.pg_class", expected)
		})
	})
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateExec(t, f, f.A, "CREATE TABLE t(id SERIAL)", 0)
		truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
		q := "TRUNCATE t RESTART IDENTITY"
		if !b.PG {
			d := truncateWant("0A000", "TRUNCATE RESTART IDENTITY is unsupported")
			d.Position = "12"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				truncateRun(t, f, f.A, s)
			}
			truncateRows(t, f, f.A, "t", 1, 2)
		} else {
			// Real PostgreSQL positive/rollback control; no native reset is implied.
			tx := truncateBegin(t, p, f.A, nil)
			truncateExec(t, f, tx, q, 0)
			truncateExec(t, f, tx, "INSERT INTO t DEFAULT VALUES", 1)
			truncateRows(t, f, tx, "t", 1)
			tx.Rollback()
			truncateRows(t, f, f.A, "t", 1, 2)
			truncateExec(t, f, f.A, q, 0)
			truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
			truncateRows(t, f, f.A, "t", 1)
		}
		d := truncateWant("42601", `syntax error at or near "junk"`)
		d.Position = "29"
		truncateError(t, f, f.A, "TRUNCATE t RESTART IDENTITY junk", d)
		// Whole-input syntax wins over unavailable native RESTART admission.
		s := fixture.ErrorDiagnostic("TRUNCATE t RESTART IDENTITY junk", d)
		s.Prepared = true
		s.ErrorStage = "prepare"
		truncateRun(t, f, f.A, s)
	})
}
func TestReferenceTruncateProviderControls(t *testing.T) {
	t.Run("TriggersAbsentNative", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			if !b.PG {
				return
			} // Native has no trigger descriptor/executor; no invented successful hook.
			for _, q := range []string{
				"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(id INT REFERENCES p)", "CREATE TABLE events(ord SERIAL,phase TEXT,rel TEXT,p_count BIGINT,c_count BIGINT)",
				"CREATE FUNCTION truncate_record() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO events(phase,rel,p_count,c_count) SELECT TG_WHEN,TG_TABLE_NAME,(SELECT count(*) FROM p),(SELECT count(*) FROM c); RETURN NULL; END $$",
				"CREATE TRIGGER p_before BEFORE TRUNCATE ON p FOR EACH STATEMENT EXECUTE FUNCTION truncate_record()",
				"CREATE TRIGGER p_after AFTER TRUNCATE ON p FOR EACH STATEMENT EXECUTE FUNCTION truncate_record()",
				"CREATE TRIGGER c_before BEFORE TRUNCATE ON c FOR EACH STATEMENT EXECUTE FUNCTION truncate_record()",
				"CREATE TRIGGER c_after AFTER TRUNCATE ON c FOR EACH STATEMENT EXECUTE FUNCTION truncate_record()",
				"CREATE TRIGGER p_delete BEFORE DELETE ON p FOR EACH STATEMENT EXECUTE FUNCTION truncate_record()",
				"CREATE TRIGGER c_delete AFTER DELETE ON c FOR EACH ROW EXECUTE FUNCTION truncate_record()",
			} {
				truncateExec(t, f, f.A, q, 0)
			}
			truncateExec(t, f, f.A, "INSERT INTO p VALUES(1),(2)", 2)
			truncateExec(t, f, f.A, "INSERT INTO c VALUES(1)", 1)
			truncateExec(t, f, f.A, "TRUNCATE p CASCADE", 0)
			truncateNotices(t, f, "c")
			truncateRun(t, f, f.A, fixture.Select("SELECT phase,rel,p_count,c_count FROM events ORDER BY ord", []string{"phase", "rel", "p_count", "c_count"}, []string{"TEXT", "TEXT", "INT8", "INT8"}, [][]any{{"BEFORE", "p", int64(2), int64(1)}, {"BEFORE", "c", int64(2), int64(1)}, {"AFTER", "p", int64(0), int64(0)}, {"AFTER", "c", int64(0), int64(0)}}))
			truncateExec(t, f, f.A, "INSERT INTO p VALUES(1)", 1)
			truncateExec(t, f, f.A, "INSERT INTO c VALUES(1)", 1)
			truncateExec(t, f, f.A, "CREATE FUNCTION truncate_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'blocked'; END $$", 0)
			truncateExec(t, f, f.A, "CREATE TRIGGER c_fail BEFORE TRUNCATE ON c FOR EACH STATEMENT EXECUTE FUNCTION truncate_fail()", 0)
			d := truncateWant("P0001", "blocked")
			d.Context = "PL/pgSQL function truncate_fail() line 1 at RAISE"
			truncateError(t, f, f.A, "TRUNCATE p CASCADE", d)
			truncateNotices(t, f, "c")
			truncateRows(t, f, f.A, "p", 1)
			truncateRows(t, f, f.A, "c", 1)
			truncateRun(t, f, f.A, fixture.Select("SELECT count(*) FROM events", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		})
	})
	t.Run("PendingTriggerEventsAbsentNative", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			if !b.PG {
				return
			} // Native deferred-trigger provider is not part of immediate FK support.
			truncateExec(t, f, f.A, "CREATE TABLE p(id INT PRIMARY KEY)", 0)
			truncateExec(t, f, f.A, "CREATE TABLE c(id INT REFERENCES p DEFERRABLE INITIALLY DEFERRED)", 0)
			tx := truncateBegin(t, p, f.A, nil)
			truncateExec(t, f, tx, "INSERT INTO p VALUES(1)", 1)
			truncateExec(t, f, tx, "INSERT INTO c VALUES(1)", 1)
			truncateError(t, f, tx, "TRUNCATE c", truncateWant("55006", `cannot TRUNCATE "c" because it has pending trigger events`))
			tx.Rollback()
			truncateRows(t, f, f.A, "p")
			truncateRows(t, f, f.A, "c")
		})
	})
	t.Run("PartitionFKAbsentNative", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			if b.PG {
				truncateExec(t, f, f.A, "CREATE TABLE p(id INT PRIMARY KEY) PARTITION BY RANGE(id)", 0)
			} else {
				// Native partitioned PK is absent; FK hierarchy admission is
				// independent and occurs before key eligibility.
				truncateExec(t, f, f.A, "CREATE TABLE p(id INT) PARTITION BY RANGE(id)", 0)
			}
			truncateExec(t, f, f.A, "CREATE TABLE leaf PARTITION OF p FOR VALUES FROM(0) TO(10)", 0)
			if !b.PG {
				truncateError(t, f, f.A, "CREATE TABLE c(id INT REFERENCES p(id))", truncateWant("0A000", "foreign keys involving partition hierarchies are unsupported"))
				return
			}
			truncateExec(t, f, f.A, "CREATE TABLE c(id INT REFERENCES p)", 0)
			truncateExec(t, f, f.A, "INSERT INTO p VALUES(1)", 1)
			truncateExec(t, f, f.A, "INSERT INTO c VALUES(1)", 1)
			truncateError(t, f, f.A, "TRUNCATE p", truncateFK("p", "c"))
			truncateExec(t, f, f.A, "TRUNCATE p CASCADE", 0)
			truncateNotices(t, f, "c")
			truncateRows(t, f, f.A, "leaf")
			truncateRows(t, f, f.A, "c")
		})
	})
	t.Run("ForeignTruncateProviderBoundary", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			if !b.PG {
				truncateExec(t, f, f.A, "CREATE FOREIGN DATA WRAPPER truncate_fdw", 0)
				defer func() {
					if _, err := f.A.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER truncate_fdw CASCADE"); err != nil {
						t.Error(err)
					}
				}()
				truncateExec(t, f, f.A, "CREATE SERVER truncate_server FOREIGN DATA WRAPPER truncate_fdw", 0)
				truncateExec(t, f, f.A, "CREATE FOREIGN TABLE ft(id INT) SERVER truncate_server", 0)
				truncateError(t, f, f.A, "TRUNCATE ft", truncateWant("0A000", "foreign table truncation is unsupported in embedded mode"))
				fixture.RunStep(t, p.Ctx, f.A, b.Recorder, fixture.Select("SELECT relkind FROM pg_class WHERE relname='ft'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"f"}}))
				return
			}
			// Genuine postgres_fdw callback and remote storage, not foreign metadata
			// pretending to be a native physical table.
			for _, q := range []string{"CREATE EXTENSION postgres_fdw", "CREATE SERVER truncate_server FOREIGN DATA WRAPPER postgres_fdw OPTIONS(host '127.0.0.1',port '5432',dbname 'stuntgres_reference')", "CREATE USER MAPPING FOR CURRENT_USER SERVER truncate_server OPTIONS(user 'stuntgres',password 'stuntgres')", "CREATE TABLE remote_t(id INT)", "CREATE FOREIGN TABLE ft(id INT) SERVER truncate_server OPTIONS(schema_name 'public',table_name 'remote_t')"} {
				truncateExec(t, f, f.A, q, 0)
			}
			defer f.A.ExecContext(p.Ctx, "DROP EXTENSION postgres_fdw CASCADE")
			truncateExec(t, f, f.A, "INSERT INTO remote_t VALUES(1),(2)", 2)
			tx := truncateBegin(t, p, f.A, nil)
			truncateExec(t, f, tx, "TRUNCATE ft CONTINUE IDENTITY", 0)
			truncateRows(t, f, tx, "ft")
			tx.Rollback()
			truncateRows(t, f, f.A, "remote_t", 1, 2)
			truncateExec(t, f, f.A, "TRUNCATE ft", 0)
			truncateRows(t, f, f.A, "remote_t")
		})
	})
	t.Run("ExistingRLSWholeTableACL", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateExec(t, f, f.A, "CREATE ROLE truncate_rls", 0)
			defer func() {
				f.A.ExecContext(p.Ctx, "RESET ROLE")
				if b.PG {
					f.A.ExecContext(p.Ctx, "DROP OWNED BY truncate_rls")
				} else {
					f.A.ExecContext(p.Ctx, "DROP TABLE IF EXISTS t")
				}
				f.A.ExecContext(p.Ctx, "DROP ROLE truncate_rls")
			}()
			for _, q := range []string{"CREATE TABLE t(id INT CONSTRAINT keep_check CHECK(id>0))", "ALTER TABLE t ENABLE ROW LEVEL SECURITY", "GRANT SELECT ON t TO truncate_rls"} {
				truncateExec(t, f, f.A, q, 0)
			}
			if b.PG {
				truncateExec(t, f, f.A, "GRANT USAGE ON SCHEMA public TO truncate_rls", 0)
			}
			var before, after int64
			probe := "SELECT oid::bigint FROM pg_constraint WHERE conname='keep_check'"
			if err := f.A.QueryRowContext(p.Ctx, probe).Scan(&before); err != nil || before <= 0 {
				t.Fatalf("CHECK identity %d: %v", before, err)
			}
			truncateExec(t, f, f.A, "INSERT INTO t VALUES(1)", 1)
			truncateExec(t, f, f.A, "SET ROLE truncate_rls", 0)
			truncateRows(t, f, f.A, "t")
			truncateError(t, f, f.A, "TRUNCATE t", truncateWant("42501", "permission denied for table t"))
			truncateExec(t, f, f.A, "RESET ROLE", 0)
			truncateExec(t, f, f.A, "GRANT TRUNCATE ON t TO truncate_rls", 0)
			truncateExec(t, f, f.A, "SET ROLE truncate_rls", 0)
			truncateExec(t, f, f.A, "TRUNCATE t", 0)
			truncateExec(t, f, f.A, "RESET ROLE", 0)
			truncateRows(t, f, f.A, "t")
			if err := f.A.QueryRowContext(p.Ctx, probe).Scan(&after); err != nil || after != before {
				t.Fatalf("CHECK reminted %d -> %d: %v", before, after, err)
			}
			diagnostic := truncateWant("23514", `new row for relation "t" violates check constraint "keep_check"`)
			diagnostic.Detail = "Failing row contains (0)."
			diagnostic.Schema = "public"
			diagnostic.Table = "t"
			diagnostic.Constraint = "keep_check"
			truncateError(t, f, f.A, "INSERT INTO t VALUES(0)", diagnostic)
		})
	})
}
func TestReferenceTruncateFeatureSmoke(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateSeed(t, f)
		truncateExec(t, f, f.A, "CREATE TABLE c(id INT REFERENCES p)", 0)
		truncateExec(t, f, f.A, "INSERT INTO c VALUES(1)", 1)
		truncateError(t, f, f.A, "TRUNCATE p RESTRICT", truncateFK("p", "c"))
		tx := truncateBegin(t, p, f.A, nil)
		truncateRun(t, f, tx, fixture.Prepared(fixture.Exec("TRUNCATE TABLE p,c CONTINUE IDENTITY", 0)))
		truncateRows(t, f, tx, "p")
		truncateRows(t, f, tx, "c")
		tx.Rollback()
		truncateRows(t, f, f.A, "p", 1, 2)
		truncateRows(t, f, f.A, "c", 1)
		truncateExec(t, f, f.A, "TRUNCATE p CASCADE", 0)
		truncateNotices(t, f, "c")
		truncateRows(t, f, f.A, "p")
		truncateRows(t, f, f.A, "c")
		truncateRows(t, f, f.A, "q", 7)
		truncateExec(t, f, f.A, "INSERT INTO p VALUES(1)", 1)
		truncateRows(t, f, f.A, "p", 1)
	})
}
