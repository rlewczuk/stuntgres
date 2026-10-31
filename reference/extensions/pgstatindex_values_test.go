package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatindexValues(t *testing.T) {
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, typ := range []string{"text", "regclass"} {
			pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex($1::"+typ+")", "ps_part.ps_index")
			pgstatindexBoth(t, p, b, c, fixture.Select("SELECT ps_part.pgstatindex($1::"+typ+")", []string{"pgstatindex"}, []string{"RECORD"}, [][]any{{"(4,0,8192,0,0,0,0,0,NaN,NaN)"}}, "ps_part.ps_index"))
			pgstatindexBoth(t, p, b, c, fixture.Select("SELECT ps_part.pgstatindex($1::"+typ+")", []string{"pgstatindex"}, []string{"RECORD"}, [][]any{{nil}}, nil))
			pgstatindexBoth(t, p, b, c, pgstatindexNull("SELECT * FROM ps_part.pgstatindex($1::"+typ+")", nil))
		}
		// Unknown literals choose the text overload, including unknown NULL.
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex('ps_part.ps_index')")
		pgstatindexBoth(t, p, b, c, pgstatindexNull("SELECT * FROM ps_part.pgstatindex(NULL)"))
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT (ps_part.pgstatindex('ps_part.ps_index'::text)).index_size AS bytes", []string{"bytes"}, []string{"INT8"}, [][]any{{int64(8192)}}))
	})
}
