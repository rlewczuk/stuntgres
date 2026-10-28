package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLFeatureSmoke(t *testing.T) {
	t.Run("serialization", xmlSerializeAcceptance)
	t.Run("boundaries", xmlBoundaryAcceptance)
	t.Run("session", xmlSessionPrepared)
	t.Run("sql", func(t *testing.T) {
		xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW xmloption", []string{"xmloption"}, []string{"TEXT"}, [][]any{{"content"}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml_feature(id integer,x xml)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml_feature VALUES(1,'<a/>')", 1))
			q := "INSERT INTO xml_feature VALUES($1,$2)"
			st, e := c.PrepareContext(p.Ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			for _, s := range []fixture.Step{fixture.Exec(q, 1, 2, "<a/><b/>"), fixture.Exec(q, 1, 3, nil)} {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, s)
			}
			xmlBoth(t, p, b, c, fixture.Select("SELECT id,x FROM xml_feature ORDER BY id", []string{"id", "x"}, []string{"INT4", "XML"}, [][]any{{int64(1), "<a/>"}, {int64(2), "<a/><b/>"}, {int64(3), nil}}))
			s := xmlSelect("SELECT x AS v FROM xml_feature ORDER BY id", "XML", "<a/>", "<a/><b/>", nil)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { xmlMetadata(t, r, b.PG, true) }
			xmlBoth(t, p, b, c, s)
			s = xmlSelect("SELECT x AS v FROM xml_feature WHERE false", "XML")
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { xmlMetadata(t, r, b.PG, true) }
			xmlBoth(t, p, b, c, s)
			literal := "SELECT XML 'text<a/>' AS v"
			held, e := c.PrepareContext(p.Ctx, literal)
			if e != nil {
				t.Fatal(e)
			}
			defer held.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION DOCUMENT", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: literal}, b.Recorder, xmlSelect(literal, "XML", "text<a/>"))
			d := xmlError("2200M", "invalid XML document", xmlFragmentDetail, 0)
			d.Context = "unnamed portal parameter $1 = '...'"
			xmlFailure(t, p, b, c, "SELECT $1::xml", d, false, "text<a/>")
			d.Context = ""
			xmlFailure(t, p, b, c, "SELECT $1::text::xml", d, false, "text<a/>")
			xmlBoth(t, p, b, c, xmlSelect("SELECT XMLPARSE(CONTENT 'text<a/>') AS v", "XML", "text<a/>"))
			xmlBoth(t, p, b, c, xmlSelect("SELECT XMLSERIALIZE(DOCUMENT '<a><b/></a>'::xml AS text INDENT) AS v", "TEXT", "<a>\n  <b/>\n</a>"))
			xmlFailure(t, p, b, c, "SELECT XMLSERIALIZE(DOCUMENT XMLPARSE(CONTENT 'text<a/>') AS text)", xmlError("2200L", "not an XML document", "", 0), false)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET xmloption", 0))
			xmlBoth(t, p, b, c, xmlSelect("SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS text NO INDENT) AS v", "TEXT", "<a/>"))
			xmlBoth(t, p, b, c, xmlSelect("SELECT '<a/>'::xml::text = '<a/>'::xml::text AS v", "BOOL", true))
			before := xmlSelect("SELECT count(*) AS v FROM xml_feature", "INT8", int64(3))
			bad := fixture.Error("INSERT INTO xml_feature VALUES(4,'<ok/>'),(5,'<bad>')", "2200N")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, bad)
			xmlBoth(t, p, b, c, before)
		})
	})
	t.Run("notices", xmlNotices)
	t.Run("controls", func(t *testing.T) { xmlScenario(t, xmlAcceptedModeControls) })
}
