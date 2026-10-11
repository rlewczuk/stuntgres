package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Each stream pins a physical session so currval and prepared execution share
// the same owner in quick mode and on the actual PostgreSQL backend.
func serialReferenceStreams(t *testing.T, sourceRoutes bool, steps func(bool) []fixture.Step) {
	t.Helper()
	lanes := []bool{false}
	if sourceRoutes {
		lanes = append(lanes, true)
	}
	for _, prepared := range []bool{false, true} {
		for _, nonContext := range lanes {
			t.Run(fmt.Sprintf("prepared=%t/source=%t", prepared, nonContext), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						db, recorder := backend.DB, backend.Recorder
						db.SetMaxOpenConns(1)
						var executor fixture.Executor = db
						if !nonContext {
							conn, err := db.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer conn.Close()
							executor = conn
						}
						for _, step := range steps(prepared) {
							step.Prepared = prepared
							step.NonContext = nonContext
							if nonContext && step.SelectRows {
								step.Prepared = false
							}
							if step.Diagnostic != nil {
								step.ErrorStage = "execute"
							}
							fixture.RunStep(t, p.Ctx, executor, recorder, step)
						}
					})
				}
			})
		}
	}
}

func TestReferenceSerialWidths(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, nonContext := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t/source=%t", prepared, nonContext), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						backend.DB.SetMaxOpenConns(1)
						var executor fixture.Executor = backend.DB
						if !nonContext {
							conn, err := backend.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer conn.Close()
							executor = conn
						}
						run := func(s fixture.Step) {
							s.Prepared = prepared
							s.NonContext = nonContext
							if nonContext && s.SelectRows && s.Query != `SELECT * FROM widths` {
								s.Prepared = false
							}
							fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
						}
						run(fixture.Exec(`CREATE TABLE widths(a SMALLSERIAL,b SERIAL,c BIGSERIAL)`, 0))
						shape := fixture.Select(`SELECT * FROM widths`, []string{"a", "b", "c"}, []string{"INT2", "INT4", "INT8"}, [][]any{})
						shape.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							types, err := rows.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							for i, typ := range types {
								scan := reflect.TypeFor[int64]()
								if backend.PG {
									scan = []reflect.Type{reflect.TypeFor[int16](), reflect.TypeFor[int32](), reflect.TypeFor[int64]()}[i]
								}
								n, known := typ.Nullable()
								if length, known := typ.Length(); length != 0 || known {
									t.Fatal("integral Length", length, known)
								}
								if typ.ScanType() != scan || n || known == backend.PG {
									t.Fatal(i, typ.ScanType(), n, known)
								}
							}
						}
						run(shape)
						const query = `INSERT INTO widths DEFAULT VALUES`
						var held *sql.Stmt
						var err error
						if nonContext {
							held, err = backend.DB.Prepare(query)
						} else {
							held, err = executor.PrepareContext(p.Ctx, query)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						run(fixture.Select(`SELECT last_value,is_called FROM widths_a_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
						for range 2 {
							if nonContext {
								r, err := held.Exec()
								fixture.DeleteReferenceCount(t, r, err, 1)
							} else {
								fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: executor, Statement: held, SQL: query}, backend.Recorder, fixture.Exec(query, 1))
							}
						}
						run(fixture.Exec(`INSERT INTO widths VALUES(DEFAULT,DEFAULT,DEFAULT),(DEFAULT,DEFAULT,DEFAULT)`, 2))
						run(fixture.Select(`SELECT * FROM widths ORDER BY a`, []string{"a", "b", "c"}, []string{"INT2", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(2), int64(2)}, {int64(3), int64(3), int64(3)}, {int64(4), int64(4), int64(4)}}))
						for i, tc := range []struct{ alias, typ string }{{"smallserial", "INT2"}, {"serial2", "INT2"}, {"serial", "INT4"}, {"serial4", "INT4"}, {"bigserial", "INT8"}, {"serial8", "INT8"}, {`"smallserial"`, "INT2"}, {`"serial"`, "INT4"}, {`"bigserial"`, "INT8"}} {
							name := fmt.Sprintf("alias%d", i)
							run(fixture.Exec(`CREATE TABLE `+name+`(id `+tc.alias+`)`, 0))
							run(fixture.Exec(`INSERT INTO `+name+` DEFAULT VALUES`, 1))
							run(fixture.Select(`SELECT * FROM `+name, []string{"id"}, []string{tc.typ}, [][]any{{int64(1)}}))
						}
					})
				}
			})
		}
	}
}

func TestReferenceSerialDemand(t *testing.T) {
	serialReferenceStreams(t, true, func(bool) []fixture.Step {
		return []fixture.Step{
			fixture.Exec(`CREATE TABLE demand(id SERIAL,v TEXT)`, 0),
			fixture.Exec(`CREATE TABLE source(v TEXT)`, 0),
			fixture.Exec(`INSERT INTO demand(v) SELECT v FROM source`, 0),
			fixture.Select(`SELECT last_value,is_called FROM demand_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
			fixture.Exec(`INSERT INTO source VALUES('a'),('b')`, 2),
			fixture.Exec(`INSERT INTO demand(v) SELECT v FROM source WHERE FALSE`, 0),
			fixture.Exec(`INSERT INTO demand(v) SELECT v FROM source LIMIT 0`, 0),
			fixture.Exec(`INSERT INTO demand(v) SELECT v FROM source ORDER BY v`, 2),
			fixture.Exec(`INSERT INTO demand VALUES(100,'explicit'),(100,'duplicate'),(0,'zero'),(-1,'negative')`, 4),
			fixture.Exec(`INSERT INTO demand OVERRIDING USER VALUE VALUES(100,'override')`, 1),
			fixture.Exec(`INSERT INTO demand VALUES(DEFAULT,'next')`, 1),
			fixture.Select(`SELECT id,v FROM demand WHERE id<100 ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), "negative"}, {int64(0), "zero"}, {int64(1), "a"}, {int64(2), "b"}, {int64(3), "next"}}),
			fixture.Select(`SELECT id,v FROM demand WHERE id=100 ORDER BY v`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(100), "duplicate"}, {int64(100), "explicit"}, {int64(100), "override"}}),
			fixture.Exec(`CREATE TABLE gaps(id SERIAL,v INT NOT NULL)`, 0),
			fixture.ImplicitError(`INSERT INTO gaps(v) VALUES(7),(NULL),(9)`, fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "gaps" violates not-null constraint`, Detail: `Failing row contains (2, null).`, Schema: "public", Table: "gaps", Column: "v"}),
			fixture.Select(`SELECT * FROM gaps`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}),
			fixture.Select(`SELECT currval('gaps_id_seq')`, []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}),
			fixture.Exec(`INSERT INTO gaps(v) VALUES(7)`, 1),
			fixture.Select(`SELECT * FROM gaps`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(7)}}),
		}
	})
}

func TestReferenceSerialDiagnostics(t *testing.T) {
	serialReferenceStreams(t, true, func(bool) []fixture.Step {
		steps := []fixture.Step{
			fixture.Exec(`CREATE TABLE nulls(id SERIAL,v TEXT)`, 0),
			fixture.ImplicitError(`INSERT INTO nulls VALUES(NULL,'bad')`, fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "nulls" violates not-null constraint`, Detail: `Failing row contains (null, bad).`, Schema: "public", Table: "nulls", Column: "id"}),
			fixture.Select(`SELECT last_value,is_called FROM nulls_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
			fixture.Exec(`INSERT INTO nulls(v) VALUES('ok')`, 1),
			fixture.Select(`SELECT * FROM nulls`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "ok"}}),
		}
		for i, width := range []struct {
			alias, typ string
			max        int64
		}{{"SMALLSERIAL", "INT2", 32767}, {"SERIAL", "INT4", 2147483647}, {"BIGSERIAL", "INT8", 9223372036854775807}} {
			name := fmt.Sprintf("limit%d", i)
			seq := name + "_id_seq"
			steps = append(steps,
				fixture.Exec(`CREATE TABLE `+name+`(id `+width.alias+`)`, 0),
				fixture.Select(fmt.Sprintf(`SELECT setval('%s',%d,false)`, seq, width.max), []string{"setval"}, []string{"INT8"}, [][]any{{width.max}}),
				fixture.Exec(`INSERT INTO `+name+` DEFAULT VALUES`, 1),
				fixture.ImplicitError(`INSERT INTO `+name+` DEFAULT VALUES`, fixture.Diagnostic{Code: "2200H", Message: fmt.Sprintf(`nextval: reached maximum value of sequence "%s" (%d)`, seq, width.max)}),
			)
			for _, value := range []int{0, -1} {
				steps = append(steps, fixture.ImplicitError(fmt.Sprintf(`SELECT setval('%s',%d)`, seq, value), fixture.Diagnostic{Code: "22003", Message: fmt.Sprintf(`setval: value %d is out of bounds for sequence "%s" (1..%d)`, value, seq, width.max)}))
			}
			steps = append(steps, fixture.Select(`SELECT * FROM `+name, []string{"id"}, []string{width.typ}, [][]any{{width.max}}), fixture.Select(`SELECT last_value,is_called FROM `+seq, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{width.max, true}}))
		}
		for i, tc := range []struct {
			alias, typ, message string
			start               int64
		}{{"SMALLSERIAL", "INT2", "smallint out of range", 32768}, {"SERIAL", "INT4", "integer out of range", 2147483648}} {
			name := fmt.Sprintf("overflow%d", i)
			steps = append(steps, fixture.Exec(`CREATE TABLE `+name+`(id `+tc.alias+`)`, 0), fixture.Exec(fmt.Sprintf(`ALTER SEQUENCE %s_id_seq AS bigint MAXVALUE 9223372036854775807 RESTART WITH %d`, name, tc.start), 0), fixture.ImplicitError(`INSERT INTO `+name+` DEFAULT VALUES`, fixture.Diagnostic{Code: "22003", Message: tc.message}), fixture.Select(`SELECT * FROM `+name, []string{"id"}, []string{tc.typ}, [][]any{}), fixture.Select(fmt.Sprintf(`SELECT currval('%s_id_seq')`, name), []string{"currval"}, []string{"INT8"}, [][]any{{tc.start}}))
		}
		return steps
	})
}

func TestReferenceSerialDependencies(t *testing.T) {
	serialReferenceStreams(t, true, func(bool) []fixture.Step {
		return []fixture.Step{
			fixture.Exec(`CREATE TABLE owned(gone INT,id SERIAL,v TEXT)`, 0),
			fixture.Exec(`INSERT INTO owned VALUES(9,40,'kept')`, 1),
			fixture.Exec(`ALTER TABLE owned DROP COLUMN gone`, 0),
			fixture.Exec(`ALTER TABLE owned DROP COLUMN id RESTRICT`, 0),
			fixture.Select(`SELECT * FROM owned`, []string{"v"}, []string{"TEXT"}, [][]any{{"kept"}}),
			fixture.Exec(`CREATE SEQUENCE owned_id_seq`, 0),
			fixture.Exec(`CREATE TABLE dep(id SERIAL)`, 0),
			fixture.Exec(`INSERT INTO dep VALUES(40)`, 1),
			fixture.ImplicitError(`DROP SEQUENCE dep_id_seq RESTRICT`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop sequence dep_id_seq because other objects depend on it`, Detail: `default value for column id of table dep depends on sequence dep_id_seq`, Hint: `Use DROP ... CASCADE to drop the dependent objects too.`}),
			fixture.Exec(`DROP SEQUENCE dep_id_seq CASCADE`, 0),
			fixture.Select(`SELECT * FROM dep`, []string{"id"}, []string{"INT4"}, [][]any{{int64(40)}}),
			fixture.ImplicitError(`INSERT INTO dep DEFAULT VALUES`, fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "dep" violates not-null constraint`, Detail: `Failing row contains (null).`, Schema: "public", Table: "dep", Column: "id"}),
			fixture.Exec(`CREATE TABLE detached(id SERIAL)`, 0),
			fixture.Exec(`ALTER SEQUENCE detached_id_seq OWNED BY NONE`, 0),
			fixture.Exec(`DROP TABLE detached RESTRICT`, 0),
			fixture.Select(`SELECT nextval('detached_id_seq')`, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}),
			fixture.Exec(`CREATE TABLE dropped(id SMALLSERIAL,b BIGSERIAL)`, 0),
			fixture.Exec(`DROP TABLE dropped RESTRICT`, 0),
			fixture.Exec(`CREATE SEQUENCE dropped_id_seq`, 0),
			fixture.Exec(`CREATE SEQUENCE dropped_b_seq`, 0),
			fixture.Exec(`CREATE TABLE removed(id SERIAL)`, 0),
			fixture.Exec(`ALTER TABLE removed ALTER id DROP DEFAULT`, 0),
			fixture.Exec(`DROP SEQUENCE removed_id_seq RESTRICT`, 0),
		}
	})
}
