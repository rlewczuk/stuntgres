package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceEncodingKeywordsControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	for _, b := range p.Backends() {
		t.Run(b.Name,func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
			if b.PG {
				if len(emitted) == 0 { t.Fatal("missing actual native dump") }
				if _,err := b.DB.ExecContext(p.Ctx,string(emitted)); err != nil { t.Fatal("actual dump replay",err) }
			} else {
				run(fixture.Exec("CREATE TABLE ek_data(id integer,encoding name)",0))
				run(fixture.Exec("INSERT INTO ek_data SELECT pg_char_to_encoding('UTF8'),pg_encoding_to_char(6)",1))
				run(fixture.Exec("CREATE VIEW ek_scalar AS SELECT pg_char_to_encoding('UTF8') AS id,pg_encoding_to_char(42) AS invalid,pg_encoding_to_char(NULL) AS absent",0))
			}
			check := func() {
				for _, prepared := range []bool{false,true} {
					for _, s := range []fixture.Step{
						fixture.Select("SELECT id,encoding FROM ek_data ORDER BY id",[]string{"id","encoding"},[]string{"INT4","NAME"},[][]any{{int64(6),"UTF8"}}),
						fixture.Select("SELECT id,invalid,absent FROM ek_scalar",[]string{"id","invalid","absent"},[]string{"INT4","NAME","NAME"},[][]any{{int64(6),"",nil}}),
						fixture.Select("SELECT count(*) AS total FROM pg_get_keywords()",[]string{"total"},[]string{"INT8"},[][]any{{int64(494)}}),
					} { s.Prepared = prepared; run(s) }
				}
			}
			check()
			// The first row could be valid, but a later coercion failure publishes none.
			run(fixture.Exec("CREATE TABLE ek_inputs(v text)",0))
			run(fixture.Exec("INSERT INTO ek_inputs VALUES ('6'),('bad')",2))
			for _, prepared := range []bool{false,true} {
				s := fixture.Error("INSERT INTO ek_data SELECT 99,pg_encoding_to_char(v::integer) FROM ek_inputs","22P02")
				s.Prepared = prepared
				run(s)
				check()
				s = fixture.Exec("INSERT INTO ek_data SELECT pg_char_to_encoding('MULE_INTERNAL'),pg_encoding_to_char(7)",1)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT id,encoding FROM ek_data WHERE id=7",[]string{"id","encoding"},[]string{"INT4","NAME"},[][]any{{int64(7),"MULE_INTERNAL"}}))
				run(fixture.Exec("DELETE FROM ek_data WHERE id=7",1))
				check()
			}
			run(fixture.Exec("DROP TABLE ek_inputs",0))
			if b.PG { return }
			emitted = fixture.ViewDump(t,p)
			if !bytes.Contains(emitted,[]byte("pg_char_to_encoding")) || !bytes.Contains(emitted,[]byte("pg_encoding_to_char")) || bytes.Contains(emitted,[]byte("pg_get_keywords")) { t.Fatal("dump is not symbolic user state",string(emitted)) }
			if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
			fixture.ViewRestore(t,p,emitted)
			check()
			connector,err := stuntgres.NewDriver().OpenConnector(t.Name()+"-fresh"); if err != nil { t.Fatal(err) }
			fresh := sql.OpenDB(connector); defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx,fresh,strings.NewReader(string(emitted))); err != nil { t.Fatal(err) }
			recorded,recorder := fixture.RecordedNativePool(t,connector)
			fixture.RunStep(t,p.Ctx,recorded,recorder,fixture.Select("SELECT id,invalid,absent FROM ek_scalar",[]string{"id","invalid","absent"},[]string{"INT4","NAME","NAME"},[][]any{{int64(6),"",nil}}))
			fixture.RunStep(t,p.Ctx,recorded,recorder,fixture.Select("SELECT id,encoding FROM ek_data",[]string{"id","encoding"},[]string{"INT4","NAME"},[][]any{{int64(6),"UTF8"}}))
			fixture.RunStep(t,p.Ctx,recorded,recorder,fixture.Select("SELECT count(*) AS total FROM pg_get_keywords()",[]string{"total"},[]string{"INT8"},[][]any{{int64(494)}}))
		})
	}
}
