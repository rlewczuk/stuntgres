package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func uuidNameConsumers(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	uuidOsspNamespaceInstall(t, p, c, r)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	for _, f := range uuidNameFunctions {
		call := "uuid_ossp_part." + f.name
		row := call + "('" + uuidNameDNS + "'::uuid,name)"
		constant := call + "('" + uuidNameDNS + "'::uuid,'www.widgets.com'::text)"
		table := f.name + "_names"
		run(fixture.Exec("CREATE TABLE "+table+"(id integer,name text,d uuid DEFAULT "+constant+",g uuid GENERATED ALWAYS AS ("+row+") STORED)", 0))
		run(fixture.Exec("CREATE INDEX "+f.name+"_names_idx ON "+table+" (("+row+"))", 0))
		run(fixture.Exec("INSERT INTO "+table+"(id,name) VALUES(1,'www.example.com'),(2,'www.widgets.com'),(3,NULL),(4,'www.example.com')", 4))
		run(fixture.Exec("CREATE VIEW "+f.name+"_names_view AS SELECT id,"+call+"(name => name,namespace => '"+uuidNameDNS+"'::uuid) AS u FROM "+table, 0))
		uuidNameConsumerRead(t, p, c, r, f.name, f.example, f.widgets)
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT "+row+" AS u,count(*) FROM "+table+" GROUP BY "+row+" ORDER BY u NULLS LAST", []string{"u", "count"}, []string{"UUID", "INT8"}, nil)
			// UUID byte order, not insertion order; a bag also avoids sorting goldens.
			s.Query = "SELECT " + row + " AS u,count(*) FROM " + table + " GROUP BY " + row
			s.Rows = [][]any{{f.example, int64(2)}, {f.widgets, int64(1)}, {nil, int64(1)}}
			s.Bag = true
			s.Prepared = prepared
			run(s)
			// A second call with different child operands must not compare equal to row.
			s = fixture.Select("SELECT "+row+" AS u,"+constant+" AS v,count(*) FROM "+table+" GROUP BY "+row+","+constant, []string{"u", "v", "count"}, []string{"UUID", "UUID", "INT8"}, [][]any{{f.example, f.widgets, int64(2)}, {f.widgets, f.widgets, int64(1)}, {nil, f.widgets, int64(1)}})
			s.Bag = true
			s.Prepared = prepared
			run(s)
			s = fixture.Select("SELECT CASE WHEN id=3 THEN NULL ELSE "+row+" END AS u FROM "+table+" WHERE id<4 ORDER BY id", []string{"u"}, []string{"UUID"}, [][]any{{f.example}, {f.widgets}, {nil}})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("UPDATE "+table+" SET name='www.widgets.com' WHERE id=4", 1))
		run(uuidNameStep("SELECT g AS u FROM "+table+" WHERE id=4", f.widgets))
		run(fixture.Exec("UPDATE "+table+" SET name='www.example.com' WHERE id=4", 1))
		// Declaration immutability must not hide volatile name children.
		volatileName := "CASE WHEN uuid_ossp_part.uuid_generate_v4() IS NULL THEN 'www.example.com' ELSE 'www.widgets.com' END"
		d := fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable")
		run(fixture.ErrorDiagnostic("CREATE TABLE "+f.name+"_bad(g uuid GENERATED ALWAYS AS ("+call+"('"+uuidNameDNS+"',"+volatileName+")) STORED)", d))
		d = fixture.ControlsReferenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE")
		run(fixture.ErrorDiagnostic("CREATE INDEX "+f.name+"_bad_idx ON "+table+" (("+call+"('"+uuidNameDNS+"',"+volatileName+")))", d))
	}
}

func uuidNameConsumerRead(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, name, example, widgets string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT d,g FROM "+name+"_names ORDER BY id", []string{"d", "g"}, []string{"UUID", "UUID"}, [][]any{{widgets, example}, {widgets, widgets}, {widgets, nil}, {widgets, example}})
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, r, s)
		s = fixture.Select("SELECT u FROM "+name+"_names_view ORDER BY id", []string{"u"}, []string{"UUID"}, [][]any{{example}, {widgets}, {nil}, {example}})
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, r, s)
		s = fixture.Select("SELECT id FROM "+name+"_names WHERE uuid_ossp_part."+name+"('"+uuidNameDNS+"'::uuid,name)='"+example+"'::uuid ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}})
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, r, s)
	}
}

func TestReferenceUUIDOsspNameConsumers(t *testing.T) {
	uuidNameCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidNameConsumers(t, p, c, r)
	})
}
