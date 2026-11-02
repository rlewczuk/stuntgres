package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsXMLIdentity(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		s.exec("CREATE TABLE pgss_part.pgss_xml_identity(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_xml_identity VALUES(1)", 1))
		queries := []string{
			"SELECT XMLROOT('<a/>'::xml,VERSION NO VALUE) AS value FROM pgss_part.pgss_xml_identity pgss_xml_root",
			"SELECT XMLROOT('<a/>'::xml,VERSION NULL) AS value FROM pgss_part.pgss_xml_identity pgss_xml_root",
		}
		pattern := "SELECT XMLROOT% FROM pgss_part.pgss_xml_identity pgss_xml_root"
		s.counts(pattern, 0, 0, 0)
		for i, query := range queries {
			step := fixture.Select(query, []string{"value"}, []string{"XML"}, [][]any{{"<a/>"}})
			if i == 1 {
				step = fixture.Prepared(step)
			}
			s.work(step)
		}
		// The released XMLROOT representation always has a typed TEXT
		// version Const; NO VALUE and explicit NULL have identical identity.
		s.counts(pattern, 1, 2, 2)
		s.entry("SELECT XMLROOT($1::xml,VERSION NO VALUE) AS value FROM pgss_part.pgss_xml_identity pgss_xml_root", 2, 2)
	})
}

func TestReferencePgStatStatementsXMLSerializeIdentity(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		s.exec("CREATE TABLE pgss_part.pgss_xml_serialize_identity(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_xml_serialize_identity VALUES(1)", 1))
		queries := []string{
			"SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS text) AS value FROM pgss_part.pgss_xml_serialize_identity pgss_xml_serialize",
			"SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS text NO INDENT) AS value FROM pgss_part.pgss_xml_serialize_identity pgss_xml_serialize",
			"SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS text INDENT) AS value FROM pgss_part.pgss_xml_serialize_identity pgss_xml_serialize",
		}
		pattern := "SELECT XMLSERIALIZE% FROM pgss_part.pgss_xml_serialize_identity pgss_xml_serialize"
		s.counts(pattern, 0, 0, 0)
		for i, query := range queries {
			step := fixture.Select(query, []string{"value"}, []string{"TEXT"}, [][]any{{"<a/>"}})
			if i != 0 {
				step = fixture.Prepared(step)
			}
			s.work(step)
		}
		// XmlExpr retains the indent Boolean: omitted and NO INDENT merge,
		// while INDENT splits even when a leaf element serializes identically.
		s.counts(pattern, 2, 3, 3)
		s.entry("SELECT XMLSERIALIZE(CONTENT $1::xml AS text) AS value FROM pgss_part.pgss_xml_serialize_identity pgss_xml_serialize", 2, 2)
		s.entry("SELECT XMLSERIALIZE(CONTENT $1::xml AS text INDENT) AS value FROM pgss_part.pgss_xml_serialize_identity pgss_xml_serialize", 1, 1)
		s.both(fixture.Select("SELECT count(DISTINCT queryid) AS identities FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())",
			[]string{"identities"}, []string{"INT8"}, [][]any{{int64(2)}}, pattern))
	})
}
