package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationOptionsAtomicity(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		relationOptionsSeed(run)
		baseline := ""
		if !b.PG {
			baseline = relationOptionsDump(t, p)
		}
		bounds := `Valid values are between "10" and "100".`
		checkDiagnostic := relationOptionsDiagnostic("23514", `check constraint "ro_bad" of relation "ro" is violated by some row`, "")
		checkDiagnostic.Schema = "public"
		checkDiagnostic.Table = "ro"
		checkDiagnostic.Constraint = "ro_bad"
		checkFailure := fixture.ErrorDiagnostic("ALTER TABLE ro SET(fillfactor=80),ADD CONSTRAINT ro_bad CHECK(id<0)", checkDiagnostic)
		checkFailure.ErrorStage = "execute"
		for _, s := range []fixture.Step{
			relationOptionsError("ALTER TABLE ro SET(fillfactor=80),SET(fillfactor=9)", "22023", `value 9 out of bounds for option "fillfactor"`, bounds),
			relationOptionsError("ALTER TABLE ro SET(fillfactor=80),ADD COLUMN id INT", "42701", `column "id" of relation "ro" already exists`, ""),
			relationOptionsError("ALTER TABLE ro ADD COLUMN id INT,SET(fillfactor=9)", "42701", `column "id" of relation "ro" already exists`, ""),
			relationOptionsError("ALTER TABLE ro SET(fillfactor=9),ADD COLUMN extra INT", "22023", `value 9 out of bounds for option "fillfactor"`, bounds),
			relationOptionsError("ALTER TABLE ro ADD COLUMN extra INT,SET(fillfactor=9)", "22023", `value 9 out of bounds for option "fillfactor"`, bounds),
			relationOptionsError("ALTER TABLE ro SET(fillfactor=9),DROP COLUMN missing", "42703", `column "missing" of relation "ro" does not exist`, ""),
			relationOptionsError("ALTER TABLE ro DROP COLUMN missing,SET(fillfactor=9)", "42703", `column "missing" of relation "ro" does not exist`, ""),
			relationOptionsError("ALTER TABLE ro SET(fillfactor=9),ALTER COLUMN payload TYPE INT USING payload::int", "22023", `value 9 out of bounds for option "fillfactor"`, bounds),
			relationOptionsError("ALTER TABLE ro ALTER COLUMN payload TYPE INT USING payload::int,SET(fillfactor=9)", "22023", `value 9 out of bounds for option "fillfactor"`, bounds),
			checkFailure,
		} {
			t.Run(s.Query, func(t *testing.T) {
				x := func(step fixture.Step) { step.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
				x(s)
				x(relationOptionsState("ro", "fillfactor=70"))
				x(relationOptionsRows("ro"))
				if !b.PG && relationOptionsDump(t, p) != baseline {
					t.Fatal("failed ALTER changed full dump")
				}
			})
		}
		run(fixture.Exec("INSERT INTO ro VALUES(NULL,NULL)", 1))
		for _, query := range []string{"ALTER TABLE ro SET(fillfactor=9),ALTER COLUMN id SET NOT NULL", "ALTER TABLE ro ALTER COLUMN id SET NOT NULL,SET(fillfactor=9)"} {
			run(relationOptionsError(query, "22023", `value 9 out of bounds for option "fillfactor"`, bounds))
			run(relationOptionsState("ro", "fillfactor=70"))
		}
	})
}

func TestReferenceRelationOptionsTransactions(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		relationOptionsSeed(run)
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		other := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER TABLE ro SET(fillfactor=80)", 0))
		run(relationOptionsState("ro", "fillfactor=80"))
		other(relationOptionsState("ro", "fillfactor=70"))
		run(fixture.Exec("COMMIT", 0))
		other(relationOptionsState("ro", "fillfactor=80"))
		committed := ""
		if !b.PG {
			committed = relationOptionsDump(t, p)
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER TABLE ro SET(fillfactor=90)", 0))
		run(relationOptionsState("ro", "fillfactor=90"))
		other(relationOptionsState("ro", "fillfactor=80"))
		if !b.PG && relationOptionsDump(t, p) != committed {
			t.Fatal("Dump leaked private options")
		}
		run(fixture.Exec("ROLLBACK", 0))
		run(relationOptionsState("ro", "fillfactor=80"))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT ro_save", 0))
		run(fixture.Exec("ALTER TABLE ro SET(fillfactor=90)", 0))
		run(fixture.Exec("INSERT INTO ro VALUES(2,'private')", 1))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT ro_save", 0))
		run(relationOptionsState("ro", "fillfactor=80"))
		run(relationOptionsRows("ro"))
		run(fixture.Exec("COMMIT", 0))
		other(relationOptionsState("ro", "fillfactor=80"))
		other(relationOptionsRows("ro"))
		run(fixture.Exec("BEGIN", 0))
		run(relationOptionsError("ALTER TABLE ro SET(fillfactor=90),SET(fillfactor=9)", "22023", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`))
		run(fixture.ErrorDiagnostic("SELECT * FROM ro", fixture.TransactionAborted()))
		run(fixture.Exec("ROLLBACK", 0))
		other(relationOptionsState("ro", "fillfactor=80"))
		other(relationOptionsRows("ro"))
	})
}

func TestReferenceRelationOptionsHierarchy(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE ro(id INT,payload TEXT)", 0))
		run(fixture.Exec("CREATE TABLE ro_child() INHERITS(ro)", 0))
		run(fixture.Exec("CREATE TABLE ro_grandchild() INHERITS(ro_child)", 0))
		run(fixture.Exec("ALTER TABLE ro SET(fillfactor=70)", 0))
		run(fixture.Exec("ALTER TABLE ro_child SET(fillfactor=80)", 0))
		run(fixture.Exec("ALTER TABLE ro_grandchild SET(fillfactor=60)", 0))
		for _, target := range []string{"ro", "ONLY ro", "ro *"} {
			run(fixture.Exec("ALTER TABLE "+target+" SET(fillfactor=90)", 0))
			run(relationOptionsState("ro", "fillfactor=90"))
			run(relationOptionsState("ro_child", "fillfactor=80"))
			run(relationOptionsState("ro_grandchild", "fillfactor=60"))
		}
		run(fixture.Exec("ALTER TABLE ro ADD COLUMN extra INT,SET(fillfactor=70)", 0))
		for _, name := range []string{"ro", "ro_child", "ro_grandchild"} {
			run(fixture.Select("SELECT * FROM ONLY "+name, []string{"id", "payload", "extra"}, []string{"INT4", "TEXT", "INT4"}, nil))
		}
		run(relationOptionsState("ro_child", "fillfactor=80"))
		run(relationOptionsState("ro_grandchild", "fillfactor=60"))
		baseline := ""
		if !b.PG {
			baseline = relationOptionsDump(t, p)
		}
		run(relationOptionsError("ALTER TABLE ro ADD COLUMN later INT,SET(fillfactor=9)", "22023", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`))
		for _, name := range []string{"ro", "ro_child", "ro_grandchild"} {
			run(fixture.Select("SELECT * FROM ONLY "+name, []string{"id", "payload", "extra"}, []string{"INT4", "TEXT", "INT4"}, nil))
		}
		run(relationOptionsState("ro", "fillfactor=70"))
		run(relationOptionsState("ro_child", "fillfactor=80"))
		run(relationOptionsState("ro_grandchild", "fillfactor=60"))
		if !b.PG && relationOptionsDump(t, p) != baseline {
			t.Fatal("failed hierarchy changed dump")
		}
		run(fixture.Exec("CREATE TABLE ro_partition(id INT,payload TEXT) PARTITION BY RANGE(id)", 0))
		run(fixture.Exec("CREATE TABLE ro_leaf PARTITION OF ro_partition FOR VALUES FROM(0) TO(10)", 0))
		run(fixture.Exec("ALTER TABLE ro_leaf SET(fillfactor=70)", 0))
		run(relationOptionsState("ro_leaf", "fillfactor=70"))
		d := relationOptionsDiagnostic("42809", "cannot specify storage parameters for a partitioned table", "")
		d.Hint = "Specify storage parameters for its leaf partitions instead."
		s := fixture.ErrorDiagnostic("ALTER TABLE ro_partition SET(fillfactor=70)", d)
		s.ErrorStage = "execute"
		run(s)
		run(relationOptionsState("ro_partition"))
		run(fixture.Exec("ALTER TABLE ro_partition RESET(fillfactor)", 0))
		run(fixture.Exec("ALTER TABLE ro_partition SET(toast.autovacuum_enabled=false)", 0))
		run(relationOptionsState("ro_partition"))
		run(relationOptionsState("ro_leaf", "fillfactor=70"))
		run(fixture.Exec("ALTER TABLE ro_partition RESET(toast.autovacuum_enabled)", 0))
		s.Query = "ALTER TABLE ro_partition ADD COLUMN extra INT,SET(fillfactor=70)"
		run(s)
		run(fixture.Select("SELECT * FROM ro_leaf", []string{"id", "payload"}, []string{"INT4", "TEXT"}, nil))
		// Supported constraint propagation remains real, with options root-only.
		run(fixture.Exec("ALTER TABLE ro_partition ADD CONSTRAINT ro_partition_positive CHECK(id>=0),SET(toast.vacuum_truncate='OFF')", 0))
		run(fixture.Exec("INSERT INTO ro_partition VALUES(1,'partition sentinel')", 1))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='ro_leaf'::regclass AND conname='ro_partition_positive'", []string{"conname"}, []string{"NAME"}, [][]any{{"ro_partition_positive"}}))
		relationOptionsToastState(t, p, b, c, prepared, "ro_leaf")
		// Otherwise-valid shape propagation is an explicit native boundary.
		// The exact same statement is successful on PostgreSQL, but the native
		// private shape/option candidates must never escape its existing guard.
		partitionBefore := ""
		if !b.PG {
			partitionBefore = relationOptionsDump(t, p)
		}
		mixed := "ALTER TABLE ro_partition ADD COLUMN boundary_extra INT,SET(toast.autovacuum_enabled=false)"
		step := fixture.Exec(mixed, 0)
		if !b.PG {
			step = relationOptionsError(mixed, "0A000", "altering partition hierarchies is unsupported", "")
		}
		run(step)
		run(relationOptionsState("ro_partition"))
		run(relationOptionsState("ro_leaf", "fillfactor=70"))
		columns := []string{"id", "payload"}
		types := []string{"INT4", "TEXT"}
		row := []any{int64(1), "partition sentinel"}
		if b.PG {
			columns = append(columns, "boundary_extra")
			types = append(types, "INT4")
			row = append(row, nil)
		}
		for _, name := range []string{"ro_partition", "ro_leaf"} {
			run(fixture.Select("SELECT * FROM "+name, columns, types, [][]any{row}))
		}
		if !b.PG && relationOptionsDump(t, p) != partitionBefore {
			t.Fatal("unsupported partition shape published private options/shape/data")
		}
		run(fixture.Exec("CREATE TYPE ro_type AS(id INT,payload TEXT)", 0))
		run(fixture.Exec("CREATE TABLE ro_typed OF ro_type", 0))
		run(fixture.Exec("ALTER TABLE ro_typed SET(fillfactor=70)", 0))
		run(fixture.TypedTableAttachment("ro_typed", "ro_type", true))
		run(relationOptionsState("ro_typed", "fillfactor=70"))
		run(fixture.Exec("CREATE TEMP TABLE ro_temp(id INT,payload TEXT)", 0))
		run(fixture.Exec("ALTER TABLE ro_temp SET(fillfactor=70)", 0))
		run(relationOptionsState("ro_temp", "fillfactor=70"))
		run(fixture.Exec("DROP TABLE ro_temp", 0))
	})
}

// Restore still owns replacement; no option statement may publish a prefix.
func relationOptionsBadRestore(t *testing.T, p *fixture.Pair, baseline string) {
	t.Helper()
	for _, tc := range []struct {
		suffix string
		want   fixture.Diagnostic
	}{
		{`ALTER TABLE "public"."ro" SET(fillfactor=80); ALTER TABLE "public"."ro" SET(fillfactor=9);`, relationOptionsDiagnostic("22023", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`)},
		{`ALTER TABLE "public"."ro" SET(fillfactor=80); ALTER TABLE "public"."ro" SET(bogus=1);`, relationOptionsDiagnostic("22023", `unrecognized parameter "bogus"`, "")},
	} {
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(baseline+tc.suffix))
		fixture.AssertDiagnostic(t, err, &tc.want)
		if relationOptionsDump(t, p) != baseline {
			t.Fatal("failed replay replaced destination")
		}
	}
}
