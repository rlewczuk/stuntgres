package stuntgres_test

import (
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func serialModes(t *testing.T, test func(*testing.T, *sql.DB, bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			db.SetMaxOpenConns(1)
			test(t, db, prepared)
		})
	}
}

func serialError(t *testing.T, db *sql.DB, prepared bool, query string, want stuntgres.Error) {
	t.Helper()
	var err error
	if prepared {
		stmt, e := db.Prepare(query)
		if e != nil {
			t.Fatal("Prepare must succeed for runtime diagnostic", query, e)
		}
		defer stmt.Close()
		_, err = stmt.Exec()
	} else {
		_, err = db.Exec(query)
	}
	typeCoreDiagnostic(t, err, want)
}

func TestSerialWidths(t *testing.T) {
	serialModes(t, func(t *testing.T, db *sql.DB, prepared bool) {
		exec := func(q string, n int64) { tableCoreExec(t, db, prepared, q, n) }
		exec(`CREATE TABLE widths(a SMALLSERIAL,b SERIAL,c BIGSERIAL)`, 0)
		tableCoreMetadata(t, db, prepared, `SELECT * FROM widths`, []string{"a", "b", "c"}, []string{"INT2", "INT4", "INT8"}, []bool{false, false, false})
		stmt, err := db.Prepare(`INSERT INTO widths DEFAULT VALUES`)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		wantTuples(t, db, `SELECT last_value,is_called FROM widths_a_seq`, [][]any{{int64(1), false}})
		for range 2 {
			r, err := stmt.Exec()
			if err != nil { t.Fatal(err) }
			if n, err := r.RowsAffected(); err != nil || n != 1 { t.Fatal(n, err) }
		}
		exec(`INSERT INTO widths VALUES(DEFAULT,DEFAULT,DEFAULT),(DEFAULT,DEFAULT,DEFAULT)`, 2)
		wantTuples(t, db, `SELECT * FROM widths ORDER BY a`, [][]any{{int64(1),int64(1),int64(1)}, {int64(2),int64(2),int64(2)}, {int64(3),int64(3),int64(3)}, {int64(4),int64(4),int64(4)}})
		for i, alias := range []string{"smallserial", "serial2", "serial", "serial4", "bigserial", "serial8", `"smallserial"`, `"serial"`, `"bigserial"`} {
			name := fmt.Sprintf("alias%d", i)
			exec(`CREATE TABLE `+name+`(id `+alias+`)`, 0)
			exec(`INSERT INTO `+name+` DEFAULT VALUES`, 1)
			wantTuples(t, db, `SELECT id FROM `+name, [][]any{{int64(1)}})
		}
	})
}

func TestSerialDemand(t *testing.T) {
	serialModes(t, func(t *testing.T, db *sql.DB, prepared bool) {
		exec := func(q string, n int64) { tableCoreExec(t, db, prepared, q, n) }
		exec(`CREATE TABLE demand(id SERIAL,v TEXT)`, 0)
		exec(`CREATE TABLE source(v TEXT)`, 0)
		exec(`INSERT INTO demand(v) SELECT v FROM source`, 0)
		wantTuples(t, db, `SELECT last_value,is_called FROM demand_id_seq`, [][]any{{int64(1), false}})
		exec(`INSERT INTO source VALUES('a'),('b')`, 2)
		exec(`INSERT INTO demand(v) SELECT v FROM source WHERE FALSE`, 0)
		exec(`INSERT INTO demand(v) SELECT v FROM source LIMIT 0`, 0)
		exec(`INSERT INTO demand(v) SELECT v FROM source ORDER BY v`, 2)
		exec(`INSERT INTO demand VALUES(100,'explicit'),(100,'duplicate'),(0,'zero'),(-1,'negative')`, 4)
		exec(`INSERT INTO demand OVERRIDING USER VALUE VALUES(100,'override')`, 1)
		exec(`INSERT INTO demand VALUES(DEFAULT,'next')`, 1)
		wantTuples(t, db, `SELECT id,v FROM demand WHERE id<100 ORDER BY id`, [][]any{{int64(-1),"negative"},{int64(0),"zero"},{int64(1),"a"},{int64(2),"b"},{int64(3),"next"}})
		exec(`CREATE TABLE gaps(id SERIAL,v INT NOT NULL)`, 0)
		serialError(t, db, prepared, `INSERT INTO gaps(v) VALUES(7),(NULL),(9)`, stuntgres.Error{Code:"23502",Message:`null value in column "v" of relation "gaps" violates not-null constraint`,Detail:`Failing row contains (2, null).`,Schema:"public",Table:"gaps",Column:"v"})
		wantTuples(t, db, `SELECT * FROM gaps`, [][]any{})
		wantTuples(t, db, `SELECT currval('gaps_id_seq')`, [][]any{{int64(2)}})
		exec(`INSERT INTO gaps(v) VALUES(7)`, 1)
		wantTuples(t, db, `SELECT * FROM gaps`, [][]any{{int64(3),int64(7)}})
	})
}

func TestSerialDiagnostics(t *testing.T) {
	serialModes(t, func(t *testing.T, db *sql.DB, prepared bool) {
		exec := func(q string, n int64) { tableCoreExec(t, db, prepared, q, n) }
		exec(`CREATE TABLE nulls(id SERIAL,v TEXT)`, 0)
		serialError(t, db, prepared, `INSERT INTO nulls VALUES(NULL,'bad')`, stuntgres.Error{Code:"23502",Message:`null value in column "id" of relation "nulls" violates not-null constraint`,Detail:`Failing row contains (null, bad).`,Schema:"public",Table:"nulls",Column:"id"})
		wantTuples(t, db, `SELECT last_value,is_called FROM nulls_id_seq`, [][]any{{int64(1),false}})
		exec(`INSERT INTO nulls(v) VALUES('ok')`, 1)
		wantTuples(t, db, `SELECT * FROM nulls`, [][]any{{int64(1),"ok"}})
		for i, width := range []struct{ alias string; max int64 }{{"SMALLSERIAL",32767},{"SERIAL",2147483647},{"BIGSERIAL",9223372036854775807}} {
			name := fmt.Sprintf("limit%d", i)
			seq := name+"_id_seq"
			exec(`CREATE TABLE `+name+`(id `+width.alias+`)`,0)
			wantTuples(t, db, fmt.Sprintf(`SELECT setval('%s',%d,false)`,seq,width.max), [][]any{{width.max}})
			exec(`INSERT INTO `+name+` DEFAULT VALUES`,1)
			serialError(t,db,prepared,`INSERT INTO `+name+` DEFAULT VALUES`,stuntgres.Error{Code:"2200H",Message:fmt.Sprintf(`nextval: reached maximum value of sequence "%s" (%d)`,seq,width.max)})
			for _, value := range []int{0,-1} {
				serialError(t,db,prepared,fmt.Sprintf(`SELECT setval('%s',%d)`,seq,value),stuntgres.Error{Code:"22003",Message:fmt.Sprintf(`setval: value %d is out of bounds for sequence "%s" (1..%d)`,value,seq,width.max)})
			}
			wantTuples(t,db,`SELECT * FROM `+name,[][]any{{width.max}})
			wantTuples(t,db,`SELECT last_value,is_called FROM `+seq,[][]any{{width.max,true}})
		}
		for i, tc := range []struct{ alias, message string; start int64 }{{"SMALLSERIAL","smallint out of range",32768},{"SERIAL","integer out of range",2147483648}} {
			name := fmt.Sprintf("overflow%d",i)
			exec(`CREATE TABLE `+name+`(id `+tc.alias+`)`,0)
			exec(fmt.Sprintf(`ALTER SEQUENCE %s_id_seq AS bigint MAXVALUE 9223372036854775807 RESTART WITH %d`,name,tc.start),0)
			serialError(t,db,prepared,`INSERT INTO `+name+` DEFAULT VALUES`,stuntgres.Error{Code:"22003",Message:tc.message})
			wantTuples(t,db,`SELECT * FROM `+name,[][]any{})
			wantTuples(t,db,fmt.Sprintf(`SELECT currval('%s_id_seq')`,name),[][]any{{tc.start}})
		}
	})
}

func TestSerialDependencies(t *testing.T) {
	serialModes(t, func(t *testing.T, db *sql.DB, prepared bool) {
		exec := func(q string, n int64) { tableCoreExec(t, db, prepared, q, n) }
		exec(`CREATE TABLE owned(gone INT,id SERIAL,v TEXT)`,0)
		exec(`INSERT INTO owned VALUES(9,40,'kept')`,1)
		exec(`ALTER TABLE owned DROP COLUMN gone`,0)
		exec(`ALTER TABLE owned DROP COLUMN id RESTRICT`,0)
		wantTuples(t,db,`SELECT * FROM owned`,[][]any{{"kept"}})
		exec(`CREATE SEQUENCE owned_id_seq`,0)
		exec(`CREATE TABLE dep(id SERIAL)`,0)
		exec(`INSERT INTO dep VALUES(40)`,1)
		serialError(t,db,prepared,`DROP SEQUENCE dep_id_seq RESTRICT`,stuntgres.Error{Code:"2BP01",Message:`cannot drop sequence dep_id_seq because other objects depend on it`,Detail:`default value for column id of table dep depends on sequence dep_id_seq`,Hint:`Use DROP ... CASCADE to drop the dependent objects too.`})
		exec(`DROP SEQUENCE dep_id_seq CASCADE`,0)
		wantTuples(t,db,`SELECT * FROM dep`,[][]any{{int64(40)}})
		serialError(t,db,prepared,`INSERT INTO dep DEFAULT VALUES`,stuntgres.Error{Code:"23502",Message:`null value in column "id" of relation "dep" violates not-null constraint`,Detail:`Failing row contains (null).`,Schema:"public",Table:"dep",Column:"id"})
		exec(`CREATE TABLE detached(id SERIAL)`,0)
		exec(`ALTER SEQUENCE detached_id_seq OWNED BY NONE`,0)
		exec(`DROP TABLE detached RESTRICT`,0)
		wantTuples(t,db,`SELECT nextval('detached_id_seq')`,[][]any{{int64(1)}})
		exec(`CREATE TABLE dropped(id SMALLSERIAL,b BIGSERIAL)`,0)
		exec(`DROP TABLE dropped RESTRICT`,0)
		exec(`CREATE SEQUENCE dropped_id_seq`,0)
		exec(`CREATE SEQUENCE dropped_b_seq`,0)
		exec(`CREATE TABLE removed(id SERIAL)`,0)
		exec(`ALTER TABLE removed ALTER id DROP DEFAULT`,0)
		exec(`DROP SEQUENCE removed_id_seq RESTRICT`,0)
	})
}
