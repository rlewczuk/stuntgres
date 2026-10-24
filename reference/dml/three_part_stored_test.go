package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceThreePartStored(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup []string
		steps []fixture.Step
	}{
		{"check", []string{"CREATE TABLE qa.stored(v int CHECK(qa.stored.v>0))"}, []fixture.Step{fixture.Exec("INSERT INTO qa.stored VALUES(10)", 1), fixture.Select("SELECT v FROM qa.stored", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})}},
		{"generated", []string{"CREATE TABLE qa.stored(v int,g int GENERATED ALWAYS AS(qa.stored.v+1) STORED)"}, []fixture.Step{fixture.Exec("INSERT INTO qa.stored(v) VALUES(10)", 1), fixture.Select("SELECT v,g FROM qa.stored", []string{"v", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(11)}})}},
		{"index", []string{"CREATE TABLE qa.stored(v int)", "CREATE INDEX three_expr ON qa.stored((qa.stored.v+1)) WHERE qa.stored.v>0"}, []fixture.Step{
			fixture.Exec("INSERT INTO qa.stored VALUES(10),(10),(-1)", 3),
			fixture.Select("SELECT v FROM qa.stored WHERE qa.stored.v+1=11 AND qa.stored.v>0 ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(10)}}),
			fixture.Select("SELECT pg_get_indexdef('qa.three_expr'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX three_expr ON qa.stored USING btree (((v + 1))) WHERE (v > 0)"}}),
			fixture.Exec("UPDATE qa.stored SET v=qa.stored.v+1 WHERE qa.stored.v>0", 2),
			fixture.Select("SELECT v FROM qa.stored ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(11)}, {int64(11)}}),
		}},
		{"partition", []string{"CREATE TABLE qa.stored(v int) PARTITION BY RANGE((qa.stored.v+1))", "CREATE TABLE qa.low PARTITION OF qa.stored FOR VALUES FROM(0) TO(10)"}, []fixture.Step{fixture.Exec("INSERT INTO qa.stored VALUES(1)", 1), fixture.Select("SELECT v FROM qa.low", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}), fixture.ImplicitError("INSERT INTO qa.stored VALUES(20)", fixture.Diagnostic{Code: "23514", Message: `no partition of relation "stored" found for row`, Detail: "Partition key of the failing row contains ((v + 1)) = (21).", Schema: "qa", Table: "stored"})}},
		{"alter", []string{"CREATE TABLE qa.stored(v int)", "INSERT INTO qa.stored VALUES(10)", "ALTER TABLE qa.stored ALTER COLUMN v TYPE bigint USING qa.stored.v+1"}, []fixture.Step{fixture.Select("SELECT v FROM qa.stored", []string{"v"}, []string{"INT8"}, [][]any{{int64(11)}})}},
		{"view_routine", []string{"CREATE TABLE qa.stored(v int)", "INSERT INTO qa.stored VALUES(10)", "CREATE VIEW qa.saved AS SELECT qa.stored.v FROM qa.stored", "CREATE FUNCTION qa.saved_value() RETURNS int LANGUAGE SQL BEGIN ATOMIC SELECT qa.stored.v FROM qa.stored; END", "ALTER TABLE qa.stored RENAME COLUMN v TO renamed"}, []fixture.Step{fixture.Select("SELECT v FROM qa.saved", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}), fixture.Select("SELECT qa.saved_value() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})}},
		{"materialized_ctas", []string{"CREATE TABLE qa.stored(v int)", "INSERT INTO qa.stored VALUES(10)", "CREATE MATERIALIZED VIEW qa.saved AS SELECT qa.stored.v FROM qa.stored", "CREATE TABLE qa.copied AS SELECT qa.stored.v FROM qa.stored"}, []fixture.Step{fixture.Select("SELECT v FROM qa.saved UNION ALL SELECT v FROM qa.copied ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(10)}})}},
		{"rule", []string{"CREATE TABLE qa.stored(v int)", "CREATE TABLE qa.audit(v int)", "CREATE RULE three_rule AS ON INSERT TO qa.stored DO ALSO INSERT INTO qa.audit SELECT new.v+qa.items.v FROM qa.items WHERE qa.items.id=1"}, []fixture.Step{fixture.Exec("INSERT INTO qa.stored VALUES(7)", 1), fixture.Select("SELECT v FROM qa.audit", []string{"v"}, []string{"INT4"}, [][]any{{int64(17)}})}},
		{"trigger", []string{"CREATE TABLE qa.stored(v int)", "CREATE FUNCTION qa.three_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v+(SELECT qa.items.v FROM qa.items WHERE qa.items.id=1); RETURN NEW; END$$", "CREATE TRIGGER three_trigger BEFORE INSERT ON qa.stored FOR EACH ROW EXECUTE FUNCTION qa.three_trigger()"}, []fixture.Step{fixture.Exec("INSERT INTO qa.stored VALUES(7)", 1), fixture.Select("SELECT v FROM qa.stored", []string{"v"}, []string{"INT4"}, [][]any{{int64(17)}})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
				threePartSeed(t, p, b)
				for _, q := range tc.setup {
					if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
						t.Fatalf("setup %q failed: %+v", q, fixture.ExtractDiagnostic(t, err))
					}
				}
				for _, s := range tc.steps {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			})
		})
	}
	t.Run("capture", threePartStoredCapture)
	t.Run("policy", threePartStoredPolicy)
}

func TestReferenceThreePartRowless(t *testing.T) {
	for _, tc := range []struct {
		query         string
		diagnostic    fixture.Diagnostic
		preparedStage string
	}{
		{"CREATE TABLE qa.defaults(v int DEFAULT qa.defaults.v)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot use column reference in DEFAULT expression", Position: "40"}, "execute"},
		{"ALTER TABLE qa.items ALTER COLUMN v SET DEFAULT qa.items.v", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot use column reference in DEFAULT expression"}, "execute"},
		{"CREATE DOMAIN qa.domain_default AS int DEFAULT qa.items.v", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot use column reference in DEFAULT expression", Position: "48"}, "execute"},
		{"CREATE DOMAIN qa.domain_check AS int CHECK(qa.items.v>0)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"items\""}, "execute"},
		{"CREATE TABLE qa.child PARTITION OF qa.parent FOR VALUES FROM(qa.items.id) TO(10)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot use column reference in partition bound expression", Position: "62"}, "execute"},
		{"INSERT INTO qa.items(id,v) VALUES(4,qa.items.v)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "37"}, "prepare"},
		{"INSERT INTO qa.items(id,v) VALUES(4,qa.items.v) ORDER BY 1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "37"}, "prepare"},
		{"INSERT INTO qa.items(id,v) SELECT 4,qa.items.v", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "37"}, "prepare"},
		{"INSERT INTO qa.items(id,v) VALUES(4,40) RETURNING qb.items.v", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"items\"", Detail: "There is an entry for table \"items\", but it cannot be referenced from this part of the query.", Position: "51"}, "prepare"},
		{"INSERT INTO qa.items(id,v) VALUES(4,40) RETURNING excluded.v", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"excluded\"", Position: "51"}, "prepare"},
		{"SELECT qa.items.v", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"items\"", Position: "8"}, "prepare"},
		{"CREATE FUNCTION qa.default_arg(x int DEFAULT qa.items.v) RETURNS int LANGUAGE SQL AS $$SELECT x$$", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"items\"", Position: "46"}, "execute"},
		{"CREATE RULE three_bad_rule AS ON INSERT TO qa.indexed WHERE qa.items.id=NEW.id DO NOTHING", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"items\"", Position: "61"}, "execute"},
		{"CREATE TRIGGER three_bad_trigger BEFORE INSERT ON qa.indexed FOR EACH ROW WHEN(qa.items.id>0) EXECUTE FUNCTION qa.context_trigger()", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"items\"", Position: "80"}, "execute"},
		{"SELECT qa.scalar_arg(qa.items.v)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"items\"", Position: "22"}, "prepare"},
		{"SELECT qa.scalar_arg(CAST(qa.items.v AS int))", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"items\"", Position: "27"}, "prepare"},
		{"EXECUTE three_rowless(qa.items.v)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: "missing FROM-clause entry for table \"items\"", Position: "23"}, "execute"},
		{"INSERT INTO qa.indexed VALUES(1,3) ON CONFLICT((qa.indexed.id)) DO UPDATE SET v=qa.indexed.v+excluded.v", fixture.Diagnostic{}, ""},
		{"INSERT INTO qa.indexed VALUES(1,3) ON CONFLICT(id) WHERE qa.indexed.id>0 DO UPDATE SET v=qa.indexed.v+excluded.v", fixture.Diagnostic{}, ""},
	} {
		q := tc.query
		t.Run(q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
						threePartSeed(t, p, b)
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						if _, err := conn.ExecContext(p.Ctx, "PREPARE three_rowless(int) AS SELECT $1"); err != nil {
							t.Fatal(err)
						}
						defer conn.ExecContext(p.Ctx, "DEALLOCATE three_rowless")
						for _, setup := range []string{
							"CREATE TABLE qa.parent(v int) PARTITION BY RANGE(v)",
							"CREATE TABLE qa.indexed(id int PRIMARY KEY,v int)",
							"INSERT INTO qa.indexed VALUES(1,10)",
							"CREATE FUNCTION qa.scalar_arg(x int) RETURNS int LANGUAGE SQL AS $$SELECT x$$",
							"CREATE FUNCTION qa.context_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
						} {
							if _, err := conn.ExecContext(p.Ctx, setup); err != nil {
								t.Fatal(err)
							}
						}
						s := fixture.Exec(q, 1)
						s.Prepared = prepared
						if tc.diagnostic.Code != "" {
							s = fixture.ErrorDiagnostic(q, tc.diagnostic)
							s.Prepared = prepared
							s.ErrorStage = "execute"
							if prepared {
								s.ErrorStage = tc.preparedStage
							}
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id,v FROM qa.items ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
						indexedValue := int64(10)
						if tc.diagnostic.Code == "" {
							indexedValue = 13
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id,v FROM qa.indexed", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), indexedValue}}))
						stateQuery := ""
						switch {
						case strings.HasPrefix(q, "CREATE TABLE qa.defaults"):
							stateQuery = "SELECT count(*) FROM pg_class WHERE relname='defaults'"
						case strings.HasPrefix(q, "CREATE TABLE qa.child"):
							stateQuery = "SELECT count(*) FROM pg_class WHERE relname='child'"
						case strings.HasPrefix(q, "CREATE DOMAIN qa.domain_default"):
							stateQuery = "SELECT count(*) FROM pg_type WHERE typname='domain_default'"
						case strings.HasPrefix(q, "CREATE DOMAIN qa.domain_check"):
							stateQuery = "SELECT count(*) FROM pg_type WHERE typname='domain_check'"
						case strings.HasPrefix(q, "CREATE FUNCTION qa.default_arg"):
							stateQuery = "SELECT count(*) FROM pg_proc WHERE proname='default_arg'"
						case strings.HasPrefix(q, "CREATE RULE three_bad_rule"):
							stateQuery = "SELECT count(*) FROM pg_rewrite WHERE rulename='three_bad_rule'"
						case strings.HasPrefix(q, "ALTER TABLE qa.items"):
							stateQuery = "SELECT count(*) FROM pg_attrdef WHERE adrelid='qa.items'::regclass"
						case strings.HasPrefix(q, "CREATE TRIGGER three_bad_trigger"):
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.ImplicitError("DROP TRIGGER three_bad_trigger ON qa.indexed", fixture.Diagnostic{Code: "42704", Message: `trigger "three_bad_trigger" for table "indexed" does not exist`}))
						}
						if stateQuery != "" {
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select(stateQuery, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
					})
				})
			}
		})
	}
}

func TestReferenceThreePartAuthorization(t *testing.T) {
	t.Run("delete", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"three_actor"})
		threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
			threePartSeed(t, p, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE three_actor", "GRANT USAGE ON SCHEMA qa,qb TO three_actor", "GRANT SELECT(id),UPDATE(v),DELETE ON qa.items TO three_actor"} {
				run(fixture.Exec(q, 0))
			}
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err = conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION three_actor"); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			q := "DELETE FROM qa.items WHERE qa.items.id=$1"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 1))
			for _, bad := range []string{"DELETE FROM qa.items WHERE qa.items.v=20", "SELECT qb.items.id FROM qb.items"} {
				s := fixture.ImplicitError(bad, fixture.Diagnostic{Code: "42501", Message: "permission denied for table items"})
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			}
			run(fixture.Exec("REVOKE SELECT(id) ON qa.items FROM three_actor", 0))
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table items"}, 2)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Select("SELECT id,v FROM qa.items ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}))
		})
	})
	t.Run("held_select_update", threePartAuthorizationHeld)
}
