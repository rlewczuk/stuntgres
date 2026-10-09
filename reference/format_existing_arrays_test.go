package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceFormatExistingArrays(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ call, want string }{
			{`format('%s/%s',VARIADIC '{1,2}'::integer[])`, "1/2"},
			{`format('%s/%s',VARIADIC '{true,false}'::boolean[])`, "t/f"},
			{`format('|%1$*2$s|',VARIADIC '{7,4}'::integer[])`, "|   7|"},
			{`format('%3$s/%1$s',VARIADIC '[0:2]={1,2,3}'::integer[])`, "3/1"},
			{`format('%L/%L',VARIADIC '{true,NULL}'::boolean[])`, "'t'/NULL"},
			{`format('ok',VARIADIC NULL::integer[])`, "ok"},
			{`format('ok',VARIADIC '{}'::boolean[])`, "ok"},
		} {
			t.Run(tc.call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(tc.call, tc.want)) })
		}
	})
}
