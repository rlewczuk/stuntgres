package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesMessages(t *testing.T) {
	for _, kind := range []string{"success", "failure", "promote"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					prefix := "CREATE DOMAIN tt_d AS integer DEFAULT 7; CREATE TYPE tt_pair AS(id integer); CREATE TABLE tt_data(v tt_d); CREATE TABLE tt_typed OF tt_pair; "
					suffix := "ALTER DOMAIN tt_d SET DEFAULT 9; ALTER TYPE tt_pair ADD ATTRIBUTE note text CASCADE; INSERT INTO tt_data DEFAULT VALUES; INSERT INTO tt_typed VALUES(7,'x')"
					switch kind {
					case "success":
						run(fixture.Exec(prefix+suffix, 1))
					case "failure":
						d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
						run(fixture.ErrorDiagnostic(prefix+suffix+"; SELECT 1/0", d))
					case "promote":
						run(fixture.Exec(prefix+"BEGIN; "+suffix, 1))
						defer c.ExecContext(p.Ctx, "ROLLBACK")
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_d','tt_pair')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						run(fixture.Exec("SAVEPOINT keep", 0))
						run(fixture.Exec("ALTER DOMAIN tt_d SET DEFAULT 11", 0))
						run(fixture.Exec("ROLLBACK TO keep", 0))
						run(fixture.Exec("COMMIT", 0))
					}
					want := int64(2)
					if kind == "failure" {
						want = 0
					}
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_d','tt_pair')", []string{"count"}, []string{"INT8"}, [][]any{{want}}))
					if kind != "failure" {
						run(fixture.Select("SELECT v FROM tt_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
						run(fixture.Select("SELECT id,note FROM tt_typed", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "x"}}))
					}
				})
			}
		})
	}
}

func TestReferenceTransactionalTypesControls(t *testing.T) {
	for _, goTx := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "sql", true: "go"}[goTx]+map[bool]string{false: "_rollback", true: "_commit"}[commit], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						var e fixture.Executor = c
						finish := func() {
							end := "ROLLBACK"
							if commit {
								end = "COMMIT"
							}
							fixture.LockExec(t, p.Ctx, c, end, 0)
						}
						if goTx {
							tx, err := c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
							finish = func() {
								var err error
								if commit {
									err = tx.Commit()
								} else {
									err = tx.Rollback()
								}
								if err != nil {
									t.Fatal(err)
								}
							}
						} else {
							fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
							defer c.ExecContext(p.Ctx, "ROLLBACK")
						}
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
						for _, q := range []string{"CREATE TYPE tt_enum AS ENUM('a')", "ALTER TYPE tt_enum ADD VALUE 'b'", "CREATE TYPE tt_range AS RANGE(subtype=integer)", "CREATE TABLE tt_control(e tt_enum,r tt_range)"} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Exec("INSERT INTO tt_control VALUES('b',tt_range(1,4))", 1))
						run(fixture.Select("SELECT e::text,lower(r),upper(r) FROM tt_control", []string{"e", "lower", "upper"}, []string{"TEXT", "INT4", "INT4"}, [][]any{{"b", int64(1), int64(4)}}))
						probe := "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_enum','tt_range')"
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(probe, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						finish()
						want := int64(0)
						if commit {
							want = 2
						}
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(probe, []string{"count"}, []string{"INT8"}, [][]any{{want}}))
					})
				}
			})
		}
	}
}
