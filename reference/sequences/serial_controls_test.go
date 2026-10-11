package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerialControls(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(db *sql.DB, rec *fixture.Recorder, s fixture.Step) {
				s.NonContext = source
				fixture.RunStep(t, p.Ctx, db, rec, s)
			}
			for _, step := range []fixture.Step{
				fixture.Exec(`CREATE TABLE called(id SERIAL,v TEXT)`, 0),
				fixture.Exec(`CREATE TABLE untouched(a SMALLSERIAL,b BIGSERIAL)`, 0),
				fixture.Exec(`INSERT INTO called VALUES(100,'duplicate'),(100,'duplicate'),(0,'zero'),(-1,'negative')`, 4),
				fixture.Select(`SELECT setval('called_id_seq',7,true)`, []string{"setval"}, []string{"INT8"}, [][]any{{int64(7)}}),
				fixture.Exec(`ALTER SEQUENCE called_id_seq RENAME TO renamed`, 0),
				fixture.Exec(`ALTER SEQUENCE renamed MAXVALUE 10000 CACHE 1`, 0),
				fixture.Exec(`COMMENT ON SEQUENCE renamed IS 'serial generator'`, 0),
			} {
				if source && step.Query == `SELECT setval('called_id_seq',7,true)` {
					step.SelectRows = false
					step.Affected = 1
				}
				run(p.Memory, p.MemoryRecord, step)
			}
			const query = `INSERT INTO called(v) VALUES('future')`
			var held *sql.Stmt
			var err error
			if source {
				held, err = p.Memory.Prepare(query)
			} else {
				held, err = p.Memory.PrepareContext(p.Ctx, query)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var old *sql.Rows
			if source {
				old, err = p.Memory.Query(`SELECT id,v FROM called ORDER BY id`)
			} else {
				old, err = p.Memory.QueryContext(p.Ctx, `SELECT id,v FROM called ORDER BY id`)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			run(p.Memory, p.MemoryRecord, fixture.Select(`SELECT last_value,is_called FROM renamed`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(7), true}}))
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("pinned replay", err)
				}
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("control definitions/state changed", dump.String(), redump.String())
			}
			if !old.Next() {
				t.Fatal(old.Err())
			}
			var id int64
			var value string
			if err := old.Scan(&id, &value); err != nil || id != -1 || value != "negative" {
				t.Fatal(id, value, err)
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			helds := map[bool]*sql.Stmt{false: held}
			if p.Postgres != nil {
				var pgHeld *sql.Stmt
				var err error
				if source {
					pgHeld, err = p.Postgres.Prepare(query)
				} else {
					pgHeld, err = p.Postgres.PrepareContext(p.Ctx, query)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer pgHeld.Close()
				helds[true] = pgHeld
			}
			for _, step := range []fixture.Step{
				fixture.Select(`SELECT last_value,is_called FROM renamed`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(7), true}}),
				fixture.Select(`SELECT last_value,is_called FROM untouched_a_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
				fixture.Exec(query, 1),
				fixture.Exec(`INSERT INTO untouched VALUES(DEFAULT,DEFAULT)`, 1),
				fixture.Select(`SELECT * FROM untouched`, []string{"a", "b"}, []string{"INT2", "INT8"}, [][]any{{int64(1), int64(1)}}),
				fixture.Select(`SELECT * FROM called ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), "negative"}, {int64(0), "zero"}, {int64(8), "future"}, {int64(100), "duplicate"}, {int64(100), "duplicate"}}),
			} {
				if source && step.Query == `INSERT INTO untouched VALUES(DEFAULT,DEFAULT)` {
					step.Query = `INSERT INTO untouched DEFAULT VALUES`
				}
				for _, backend := range p.Backends() {
					if source {
						if step.Query == query {
							r, err := helds[backend.PG].Exec()
							fixture.DeleteReferenceCount(t, r, err, 1)
						} else {
							run(backend.DB, backend.Recorder, step)
						}
					} else {
						executor := fixture.HeldStatement{Executor: backend.DB, Statement: helds[backend.PG], SQL: query}
						fixture.RunStep(t, p.Ctx, executor, backend.Recorder, step)
					}
				}
			}
			for _, script := range []string{`CREATE TABLE broken(id INT DEFAULT nextval('missing'));`, `CREATE TABLE notseq(id INT); CREATE TABLE broken(id INT DEFAULT nextval('notseq'));`, `CREATE TABLE broken(id SERIAL); SELECT setval('broken_id_seq',0,true);`} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
					t.Fatal("invalid Restore succeeded", script)
				}
				run(p.Memory, p.MemoryRecord, fixture.Select(`SELECT * FROM untouched`, []string{"a", "b"}, []string{"INT2", "INT8"}, [][]any{{int64(1), int64(1)}}))
			}
			for _, backend := range p.Backends() {
				if source {
					r, err := helds[backend.PG].Exec()
					fixture.DeleteReferenceCount(t, r, err, 1)
				} else {
					executor := fixture.HeldStatement{Executor: backend.DB, Statement: helds[backend.PG], SQL: query}
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Exec(query, 1))
				}
				run(backend.DB, backend.Recorder, fixture.Select(`SELECT id FROM called WHERE id<100 AND id>0 ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(9)}}))
			}
			for _, step := range []fixture.Step{
				fixture.ImplicitError(`DROP SEQUENCE renamed RESTRICT`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop sequence renamed because other objects depend on it`, Detail: `default value for column id of table called depends on sequence renamed`, Hint: `Use DROP ... CASCADE to drop the dependent objects too.`}),
				fixture.Exec(`DROP TABLE called RESTRICT`, 0),
				fixture.Exec(`CREATE SEQUENCE renamed`, 0),
			} {
				p.Step(t, step)
				// The legacy dependency complement remains recorded, not a new source vector.
			}
		})
	}
}
