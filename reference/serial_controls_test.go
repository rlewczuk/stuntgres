package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceSerialControls(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			p := newReferenceScenario(t)
			run := func(db *sql.DB, rec *referenceRecorder, s referenceStep) {
				s.nonContext = source
				referenceRunStep(t, p.ctx, db, rec, s)
			}
			for _, step := range []referenceStep{
				referenceExec(`CREATE TABLE called(id SERIAL,v TEXT)`, 0),
				referenceExec(`CREATE TABLE untouched(a SMALLSERIAL,b BIGSERIAL)`, 0),
				referenceExec(`INSERT INTO called VALUES(100,'duplicate'),(100,'duplicate'),(0,'zero'),(-1,'negative')`, 4),
				referenceSelect(`SELECT setval('called_id_seq',7,true)`, []string{"setval"}, []string{"INT8"}, [][]any{{int64(7)}}),
				referenceExec(`ALTER SEQUENCE called_id_seq RENAME TO renamed`, 0),
				referenceExec(`ALTER SEQUENCE renamed MAXVALUE 10000 CACHE 1`, 0),
				referenceExec(`COMMENT ON SEQUENCE renamed IS 'serial generator'`, 0),
			} {
				if source && step.query == `SELECT setval('called_id_seq',7,true)` {
					step.selectRows = false
					step.affected = 1
				}
				run(p.memory, p.memoryRecord, step)
			}
			const query = `INSERT INTO called(v) VALUES('future')`
			var held *sql.Stmt
			var err error
			if source {
				held, err = p.memory.Prepare(query)
			} else {
				held, err = p.memory.PrepareContext(p.ctx, query)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var old *sql.Rows
			if source {
				old, err = p.memory.Query(`SELECT id,v FROM called ORDER BY id`)
			} else {
				old, err = p.memory.QueryContext(p.ctx, `SELECT id,v FROM called ORDER BY id`)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			run(p.memory, p.memoryRecord, referenceSelect(`SELECT last_value,is_called FROM renamed`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(7), true}}))
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal("pinned replay", err)
				}
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
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
			if p.postgres != nil {
				var pgHeld *sql.Stmt
				var err error
				if source {
					pgHeld, err = p.postgres.Prepare(query)
				} else {
					pgHeld, err = p.postgres.PrepareContext(p.ctx, query)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer pgHeld.Close()
				helds[true] = pgHeld
			}
			for _, step := range []referenceStep{
				referenceSelect(`SELECT last_value,is_called FROM renamed`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(7), true}}),
				referenceSelect(`SELECT last_value,is_called FROM untouched_a_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
				referenceExec(query, 1),
				referenceExec(`INSERT INTO untouched VALUES(DEFAULT,DEFAULT)`, 1),
				referenceSelect(`SELECT * FROM untouched`, []string{"a", "b"}, []string{"INT2", "INT8"}, [][]any{{int64(1), int64(1)}}),
				referenceSelect(`SELECT * FROM called ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), "negative"}, {int64(0), "zero"}, {int64(8), "future"}, {int64(100), "duplicate"}, {int64(100), "duplicate"}}),
			} {
				if source && step.query == `INSERT INTO untouched VALUES(DEFAULT,DEFAULT)` {
					step.query = `INSERT INTO untouched DEFAULT VALUES`
				}
				for _, backend := range p.backends() {
					if source {
						if step.query == query {
							r, err := helds[backend.pg].Exec()
							deleteReferenceCount(t, r, err, 1)
						} else {
							run(backend.db, backend.recorder, step)
						}
					} else {
						executor := referenceHeldStatement{referenceExecutor: backend.db, statement: helds[backend.pg], query: query}
						referenceRunStep(t, p.ctx, executor, backend.recorder, step)
					}
				}
			}
			for _, script := range []string{`CREATE TABLE broken(id INT DEFAULT nextval('missing'));`, `CREATE TABLE notseq(id INT); CREATE TABLE broken(id INT DEFAULT nextval('notseq'));`, `CREATE TABLE broken(id SERIAL); SELECT setval('broken_id_seq',0,true);`} {
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
					t.Fatal("invalid Restore succeeded", script)
				}
				run(p.memory, p.memoryRecord, referenceSelect(`SELECT * FROM untouched`, []string{"a", "b"}, []string{"INT2", "INT8"}, [][]any{{int64(1), int64(1)}}))
			}
			for _, backend := range p.backends() {
				if source {
					r, err := helds[backend.pg].Exec()
					deleteReferenceCount(t, r, err, 1)
				} else {
					executor := referenceHeldStatement{referenceExecutor: backend.db, statement: helds[backend.pg], query: query}
					referenceRunStep(t, p.ctx, executor, backend.recorder, referenceExec(query, 1))
				}
				run(backend.db, backend.recorder, referenceSelect(`SELECT id FROM called WHERE id<100 AND id>0 ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(9)}}))
			}
			for _, step := range []referenceStep{
				referenceImplicitError(`DROP SEQUENCE renamed RESTRICT`, referenceDiagnostic{Code: "2BP01", Message: `cannot drop sequence renamed because other objects depend on it`, Detail: `default value for column id of table called depends on sequence renamed`, Hint: `Use DROP ... CASCADE to drop the dependent objects too.`}),
				referenceExec(`DROP TABLE called RESTRICT`, 0),
				referenceExec(`CREATE SEQUENCE renamed`, 0),
			} {
				p.step(t, step)
				// The legacy dependency complement remains recorded, not a new source vector.
			}
		})
	}
}
