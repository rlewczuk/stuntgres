//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

// Run complete independent streams, not alternating steps: an unsupported native
// CREATE must not prevent the pinned PostgreSQL branch from reaching its oracle.
func serialReferenceStreams(t *testing.T, steps func(bool) []referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false,true} {
		t.Run(fmt.Sprint(prepared),func(t *testing.T) {
			p := newReferencePair(t)
			for _, postgres := range []bool{true,false} {
				t.Run(map[bool]string{true:"postgres",false:"memory"}[postgres],func(t *testing.T) {
					db, recorder := p.memory,p.memoryRecord
					if postgres { db,recorder = p.postgres,p.postgresRecord }
					conn,err := db.Conn(p.ctx)
					if err != nil { t.Fatal(err) }
					defer conn.Close()
					for _, step := range steps(prepared) {
						step.prepared = prepared
						referenceRunStep(t,p.ctx,conn,recorder,step)
					}
				})
			}
		})
	}
}

func TestReferenceSerialWidths(t *testing.T) {
	serialReferenceStreams(t,func(bool) []referenceStep {
		steps := []referenceStep{
			referenceExec(`CREATE TABLE widths(a SMALLSERIAL,b SERIAL,c BIGSERIAL)`,0),
			referenceSelect(`SELECT * FROM widths`,[]string{"a","b","c"},[]string{"INT2","INT4","INT8"},[][]any{}),
			referenceSelect(`SELECT last_value,is_called FROM widths_a_seq`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(1),false}}),
			referenceExec(`INSERT INTO widths DEFAULT VALUES`,1),
			referenceExec(`INSERT INTO widths DEFAULT VALUES`,1),
			referenceExec(`INSERT INTO widths VALUES(DEFAULT,DEFAULT,DEFAULT),(DEFAULT,DEFAULT,DEFAULT)`,2),
			referenceSelect(`SELECT * FROM widths ORDER BY a`,[]string{"a","b","c"},[]string{"INT2","INT4","INT8"},[][]any{{int64(1),int64(1),int64(1)},{int64(2),int64(2),int64(2)},{int64(3),int64(3),int64(3)},{int64(4),int64(4),int64(4)}}),
		}
		for i,tc := range []struct{ alias,typ string }{{"smallserial","INT2"},{"serial2","INT2"},{"serial","INT4"},{"serial4","INT4"},{"bigserial","INT8"},{"serial8","INT8"},{`"smallserial"`,"INT2"},{`"serial"`,"INT4"},{`"bigserial"`,"INT8"}} {
			name := fmt.Sprintf("alias%d",i)
			steps = append(steps,referenceExec(`CREATE TABLE `+name+`(id `+tc.alias+`)`,0),referenceExec(`INSERT INTO `+name+` DEFAULT VALUES`,1),referenceSelect(`SELECT * FROM `+name,[]string{"id"},[]string{tc.typ},[][]any{{int64(1)}}))
		}
		return steps
	})
}

func TestReferenceSerialDemand(t *testing.T) {
	serialReferenceStreams(t,func(bool) []referenceStep { return []referenceStep{
		referenceExec(`CREATE TABLE demand(id SERIAL,v TEXT)`,0),
		referenceExec(`CREATE TABLE source(v TEXT)`,0),
		referenceExec(`INSERT INTO demand(v) SELECT v FROM source`,0),
		referenceSelect(`SELECT last_value,is_called FROM demand_id_seq`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(1),false}}),
		referenceExec(`INSERT INTO source VALUES('a'),('b')`,2),
		referenceExec(`INSERT INTO demand(v) SELECT v FROM source WHERE FALSE`,0),
		referenceExec(`INSERT INTO demand(v) SELECT v FROM source LIMIT 0`,0),
		referenceExec(`INSERT INTO demand(v) SELECT v FROM source ORDER BY v`,2),
		referenceExec(`INSERT INTO demand VALUES(100,'explicit'),(100,'duplicate'),(0,'zero'),(-1,'negative')`,4),
		referenceExec(`INSERT INTO demand OVERRIDING USER VALUE VALUES(100,'override')`,1),
		referenceExec(`INSERT INTO demand VALUES(DEFAULT,'next')`,1),
		referenceSelect(`SELECT id,v FROM demand WHERE id<100 ORDER BY id`,[]string{"id","v"},[]string{"INT4","TEXT"},[][]any{{int64(-1),"negative"},{int64(0),"zero"},{int64(1),"a"},{int64(2),"b"},{int64(3),"next"}}),
		referenceSelect(`SELECT id,v FROM demand WHERE id=100 ORDER BY v`,[]string{"id","v"},[]string{"INT4","TEXT"},[][]any{{int64(100),"duplicate"},{int64(100),"explicit"},{int64(100),"override"}}),
		referenceExec(`CREATE TABLE gaps(id SERIAL,v INT NOT NULL)`,0),
		referenceImplicitError(`INSERT INTO gaps(v) VALUES(7),(NULL),(9)`,referenceDiagnostic{Code:"23502",Message:`null value in column "v" of relation "gaps" violates not-null constraint`,Detail:`Failing row contains (2, null).`,Schema:"public",Table:"gaps",Column:"v"}),
		referenceSelect(`SELECT * FROM gaps`,[]string{"id","v"},[]string{"INT4","INT4"},[][]any{}),
		referenceSelect(`SELECT currval('gaps_id_seq')`,[]string{"currval"},[]string{"INT8"},[][]any{{int64(2)}}),
		referenceExec(`INSERT INTO gaps(v) VALUES(7)`,1),
		referenceSelect(`SELECT * FROM gaps`,[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(3),int64(7)}}),
	} })
}

func TestReferenceSerialDiagnostics(t *testing.T) {
	serialReferenceStreams(t,func(bool) []referenceStep {
		steps := []referenceStep{
			referenceExec(`CREATE TABLE nulls(id SERIAL,v TEXT)`,0),
			referenceImplicitError(`INSERT INTO nulls VALUES(NULL,'bad')`,referenceDiagnostic{Code:"23502",Message:`null value in column "id" of relation "nulls" violates not-null constraint`,Detail:`Failing row contains (null, bad).`,Schema:"public",Table:"nulls",Column:"id"}),
			referenceSelect(`SELECT last_value,is_called FROM nulls_id_seq`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(1),false}}),
			referenceExec(`INSERT INTO nulls(v) VALUES('ok')`,1),
			referenceSelect(`SELECT * FROM nulls`,[]string{"id","v"},[]string{"INT4","TEXT"},[][]any{{int64(1),"ok"}}),
		}
		for i,width := range []struct{ alias,typ string; max int64 }{{"SMALLSERIAL","INT2",32767},{"SERIAL","INT4",2147483647},{"BIGSERIAL","INT8",9223372036854775807}} {
			name := fmt.Sprintf("limit%d",i)
			seq := name+"_id_seq"
			steps = append(steps,
				referenceExec(`CREATE TABLE `+name+`(id `+width.alias+`)`,0),
				referenceSelect(fmt.Sprintf(`SELECT setval('%s',%d,false)`,seq,width.max),[]string{"setval"},[]string{"INT8"},[][]any{{width.max}}),
				referenceExec(`INSERT INTO `+name+` DEFAULT VALUES`,1),
				referenceImplicitError(`INSERT INTO `+name+` DEFAULT VALUES`,referenceDiagnostic{Code:"2200H",Message:fmt.Sprintf(`nextval: reached maximum value of sequence "%s" (%d)`,seq,width.max)}),
			)
			for _,value := range []int{0,-1} {
				steps = append(steps,referenceImplicitError(fmt.Sprintf(`SELECT setval('%s',%d)`,seq,value),referenceDiagnostic{Code:"22003",Message:fmt.Sprintf(`setval: value %d is out of bounds for sequence "%s" (1..%d)`,value,seq,width.max)}))
			}
			steps = append(steps,referenceSelect(`SELECT * FROM `+name,[]string{"id"},[]string{width.typ},[][]any{{width.max}}),referenceSelect(`SELECT last_value,is_called FROM `+seq,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{width.max,true}}))
		}
		for i,tc := range []struct{ alias,typ,message string; start int64 }{{"SMALLSERIAL","INT2","smallint out of range",32768},{"SERIAL","INT4","integer out of range",2147483648}} {
			name := fmt.Sprintf("overflow%d",i)
			steps = append(steps,referenceExec(`CREATE TABLE `+name+`(id `+tc.alias+`)`,0),referenceExec(fmt.Sprintf(`ALTER SEQUENCE %s_id_seq AS bigint MAXVALUE 9223372036854775807 RESTART WITH %d`,name,tc.start),0),referenceImplicitError(`INSERT INTO `+name+` DEFAULT VALUES`,referenceDiagnostic{Code:"22003",Message:tc.message}),referenceSelect(`SELECT * FROM `+name,[]string{"id"},[]string{tc.typ},[][]any{}),referenceSelect(fmt.Sprintf(`SELECT currval('%s_id_seq')`,name),[]string{"currval"},[]string{"INT8"},[][]any{{tc.start}}))
		}
		return steps
	})
}

func TestReferenceSerialDependencies(t *testing.T) {
	serialReferenceStreams(t,func(bool) []referenceStep { return []referenceStep{
		referenceExec(`CREATE TABLE owned(gone INT,id SERIAL,v TEXT)`,0),
		referenceExec(`INSERT INTO owned VALUES(9,40,'kept')`,1),
		referenceExec(`ALTER TABLE owned DROP COLUMN gone`,0),
		referenceExec(`ALTER TABLE owned DROP COLUMN id RESTRICT`,0),
		referenceSelect(`SELECT * FROM owned`,[]string{"v"},[]string{"TEXT"},[][]any{{"kept"}}),
		referenceExec(`CREATE SEQUENCE owned_id_seq`,0),
		referenceExec(`CREATE TABLE dep(id SERIAL)`,0),
		referenceExec(`INSERT INTO dep VALUES(40)`,1),
		referenceImplicitError(`DROP SEQUENCE dep_id_seq RESTRICT`,referenceDiagnostic{Code:"2BP01",Message:`cannot drop sequence dep_id_seq because other objects depend on it`,Detail:`default value for column id of table dep depends on sequence dep_id_seq`,Hint:`Use DROP ... CASCADE to drop the dependent objects too.`}),
		referenceExec(`DROP SEQUENCE dep_id_seq CASCADE`,0),
		referenceSelect(`SELECT * FROM dep`,[]string{"id"},[]string{"INT4"},[][]any{{int64(40)}}),
		referenceImplicitError(`INSERT INTO dep DEFAULT VALUES`,referenceDiagnostic{Code:"23502",Message:`null value in column "id" of relation "dep" violates not-null constraint`,Detail:`Failing row contains (null).`,Schema:"public",Table:"dep",Column:"id"}),
		referenceExec(`CREATE TABLE detached(id SERIAL)`,0),
		referenceExec(`ALTER SEQUENCE detached_id_seq OWNED BY NONE`,0),
		referenceExec(`DROP TABLE detached RESTRICT`,0),
		referenceSelect(`SELECT nextval('detached_id_seq')`,[]string{"nextval"},[]string{"INT8"},[][]any{{int64(1)}}),
		referenceExec(`CREATE TABLE dropped(id SMALLSERIAL,b BIGSERIAL)`,0),
		referenceExec(`DROP TABLE dropped RESTRICT`,0),
		referenceExec(`CREATE SEQUENCE dropped_id_seq`,0),
		referenceExec(`CREATE SEQUENCE dropped_b_seq`,0),
		referenceExec(`CREATE TABLE removed(id SERIAL)`,0),
		referenceExec(`ALTER TABLE removed ALTER id DROP DEFAULT`,0),
		referenceExec(`DROP SEQUENCE removed_id_seq RESTRICT`,0),
	} })
}
