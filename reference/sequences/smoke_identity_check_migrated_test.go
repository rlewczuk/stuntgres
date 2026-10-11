package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeIdentityCheckControlsSmoke(t *testing.T) {
	for _, scenario := range []struct{ name, create, table, sequence string }{
		{"automatic", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('i_id_seq')>0))`, "i", "i_id_seq"},
		{"explicit", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME owncheck),CHECK(nextval('owncheck')>0))`, "i", "owncheck"},
		{"schema", `CREATE SCHEMA portable CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('portable.i_id_seq')>0))`, "portable.i", "portable.i_id_seq"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "portable")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				func() {
					db := b.DB
					run := func(step fixture.Step) { step.NonContext = true; fixture.RunStep(t, p.Ctx, db, b.Recorder, step) }
					db.SetMaxOpenConns(1)
					run(fixture.Exec(scenario.create, 0))
					state := `SELECT last_value,is_called FROM ` + scenario.sequence
					run(fixture.Select(state, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
					if !b.PG {
						var empty bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &empty); err != nil {
							t.Fatal(err)
						}
						run(fixture.Select(state, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(empty.Bytes())); err != nil {
							t.Fatal(err)
						}
					}
					run(fixture.Exec(`INSERT INTO `+scenario.table+` DEFAULT VALUES`, 1))
					run(fixture.Select(`SELECT id FROM `+scenario.table, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					run(fixture.Select(state, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
					run(fixture.Select(`SELECT currval('`+scenario.sequence+`'),lastval()`, []string{"currval", "lastval"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
					held, err := db.Prepare(`INSERT INTO ` + scenario.table + ` DEFAULT VALUES`)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					run(fixture.Select(state, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
					if !b.PG {
						var dump, again bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(again.Bytes())); err != nil {
							t.Fatal(err)
						}
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: db, Statement: held, SQL: `INSERT INTO ` + scenario.table + ` DEFAULT VALUES`}, b.Recorder, smokeIdentityNonContext(fixture.Exec(`INSERT INTO `+scenario.table+` DEFAULT VALUES`, 1)))
					run(fixture.Select(`SELECT id FROM `+scenario.table+` ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
					run(fixture.Select(state, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(4), true}}))
					run(fixture.Exec(`DROP TABLE `+scenario.table, 0))
					rows, missingErr := db.Query(`SELECT last_value FROM ` + scenario.sequence)
					if rows != nil {
						rows.Close()
					}
					smokeIdentityState(t, missingErr, "42P01")
					_, err = db.Exec(`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('bad_id_seq')>0),CHECK(missing>0))`)
					smokeIdentityState(t, err, "42703")
					run(fixture.Select(`SELECT count(*) FROM pg_catalog.pg_class WHERE relname='bad' OR relname='bad_id_seq'`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Exec(`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY)`, 0))
					run(fixture.Select(`SELECT last_value,is_called FROM bad_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
					if scenario.name == "schema" {
						run(fixture.Exec(`DROP SCHEMA portable CASCADE`, 0))
					}
				}()
			}
		})
	}
	t.Run("owner collision", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "portable")
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			func() {
				source := b.DB
				dest := source
				runSource := func(step fixture.Step) { step.NonContext = true; fixture.RunStep(t, p.Ctx, source, b.Recorder, step) }
				if !b.PG {
					connector, err := stuntgres.NewDriver().OpenConnector("reference/" + t.Name())
					if err != nil {
						t.Fatal(err)
					}
					dest = sql.OpenDB(connector)
					defer dest.Close()
				}
				run := func(step fixture.Step) { step.NonContext = true; fixture.RunStep(t, p.Ctx, dest, b.Recorder, step) }
				// Distinct native drivers own distinct catalogs despite their equal DSNs.
				for _, q := range []string{`CREATE SCHEMA portable`, `CREATE TABLE portable.i(id INT GENERATED ALWAYS AS IDENTITY)`, `CREATE VIEW portable.v AS SELECT last_value FROM portable.i_id_seq`, `CREATE TABLE portable.c(p INT CHECK(nextval('portable.i_id_seq')>0))`} {
					runSource(fixture.Exec(q, 0))
				}
				runSource(fixture.Exec(`INSERT INTO portable.i DEFAULT VALUES`, 1))
				ctx := p.Ctx
				var kept *sql.Conn
				var err error
				var value int64
				if !b.PG {
					kept, err = dest.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer kept.Close()
					if _, err := kept.ExecContext(ctx, `CREATE TEMP SEQUENCE kept START 30 CACHE 10`); err != nil {
						t.Fatal(err)
					}
					if err := kept.QueryRowContext(ctx, `SELECT nextval('kept')`).Scan(&value); err != nil || value != 30 {
						t.Fatal(value, err)
					}
					run(fixture.Exec(`CREATE TABLE displaced(v INT)`, 0))
					run(fixture.Exec(`CREATE TABLE displaced2(v INT)`, 0))
					var initial, dump, again bytes.Buffer
					if err := stuntgres.Dump(ctx, p.Native, &initial); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(ctx, dest, bytes.NewReader(initial.Bytes())); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Dump(ctx, dest, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Dump(ctx, dest, &again); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(dump.Bytes(), again.Bytes()) {
						t.Fatal("owner collision redump changed")
					}
				}
				run(fixture.Select(`SELECT id FROM portable.i`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(fixture.Select(`SELECT last_value FROM portable.v`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
				held, err := dest.Prepare(`INSERT INTO portable.c VALUES(7)`)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: dest, Statement: held, SQL: `INSERT INTO portable.c VALUES(7)`}, b.Recorder, smokeIdentityNonContext(fixture.Exec(`INSERT INTO portable.c VALUES(7)`, 1)))
				held.Close()
				run(fixture.Exec(`INSERT INTO portable.i DEFAULT VALUES`, 1))
				run(fixture.Select(`SELECT id FROM portable.i ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
				run(fixture.Select(`SELECT last_value FROM portable.v`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(3)}}))
				_, err = dest.Exec(`ALTER TABLE portable.i ALTER id DROP IDENTITY`)
				smokeIdentityState(t, err, "2BP01")
				run(fixture.Exec(`DROP TABLE portable.i CASCADE`, 0))
				run(fixture.Exec(`INSERT INTO portable.c VALUES(9)`, 1))
				run(fixture.Select(`SELECT p FROM portable.c ORDER BY p`, []string{"p"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
				rows, missingErr := dest.Query(`SELECT last_value FROM portable.i_id_seq`)
				if rows != nil {
					rows.Close()
				}
				smokeIdentityState(t, missingErr, "42P01")
				if !b.PG {
					if err := kept.QueryRowContext(ctx, `SELECT nextval('kept')`).Scan(&value); err != nil || value != 31 {
						t.Fatal(value, err)
					}
				}
				// Ordinary sequence providers remain prefix-created, not graph vertices.
				run(fixture.Exec(`CREATE SEQUENCE ordinary`, 0))
				run(fixture.Exec(`CREATE TABLE ordinary_c(p INT CHECK(nextval('ordinary')>0))`, 0))
				run(fixture.Exec(`CREATE VIEW ordinary_v AS SELECT last_value FROM ordinary`, 0))
				if !b.PG {
					var ordinary bytes.Buffer
					if err := stuntgres.Dump(ctx, dest, &ordinary); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(ctx, dest, bytes.NewReader(ordinary.Bytes())); err != nil {
						t.Fatal(err)
					}
				}
				run(fixture.Exec(`INSERT INTO ordinary_c VALUES(4)`, 1))
				run(fixture.Select(`SELECT last_value FROM ordinary_v`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(fixture.Exec(`DROP SCHEMA portable CASCADE`, 0))
			}()
		}
	})
}
