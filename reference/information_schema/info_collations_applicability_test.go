package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCollationsApplicability(t *testing.T) {
	icoScenario(t, false, func(c *icoCase) {
		encodings := []int64{-1,-1,-1,6,6,6,-1}
		var want [][]any
		for i, name := range icoNames { want = append(want, []any{name, encodings[i], int64(6), "UTF8", "NO PAD"}) }
		q := `SELECT i.collation_name,c.collencoding,d.encoding,s.character_set_name,i.pad_attribute FROM information_schema.collations i JOIN pg_catalog.pg_namespace n ON n.nspname=i.collation_schema JOIN pg_catalog.pg_collation c ON c.collnamespace=n.oid AND c.collname=i.collation_name JOIN pg_catalog.pg_database d ON d.datname=i.collation_catalog JOIN information_schema.character_sets s ON s.character_set_name='UTF8' WHERE i.collation_schema='pg_catalog' AND i.collation_name IN ('C','POSIX','default','pg_c_utf8','pg_unicode_fast','ucs_basic','unicode') AND d.datname=current_database() ORDER BY i.collation_name`
		c.routes(fixture.Select(q, []string{"collation_name", "collencoding", "encoding", "character_set_name", "pad_attribute"}, []string{"NAME", "INT4", "INT4", "NAME", "VARCHAR"}, want))
		q = `SELECT i.collation_name FROM information_schema.collations i JOIN pg_catalog.pg_namespace n ON n.nspname=i.collation_schema JOIN pg_catalog.pg_collation c ON c.collnamespace=n.oid AND c.collname=i.collation_name JOIN pg_catalog.pg_database d ON d.datname=i.collation_catalog WHERE d.datname=current_database() AND c.collencoding NOT IN (-1,d.encoding)`
		c.routes(fixture.Select(q, []string{"collation_name"}, []string{"NAME"}, nil))
	})
}
