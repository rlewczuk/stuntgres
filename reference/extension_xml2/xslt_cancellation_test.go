package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTCancellation(t *testing.T) {
	for _, arity := range []string{"two", "three"} {
		t.Run(arity, func(t *testing.T) {
			xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				xml2Install(t, p, b, c)
				for _, q := range []string{"CREATE TABLE xslt_cancel_source(id integer,doc text)", "CREATE TABLE xslt_cancel_sink(id integer,v text)"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				large := "<r>" + strings.Repeat("<n/>", 12000) + "</r>"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xslt_cancel_source VALUES(1,'<r/>'),(2,$1::text)", 2, large))
				style := xsltSmokeStyle(xsltTextOutput, "", `<xsl:for-each select="r/n"><xsl:value-of select="count(preceding-sibling::n)"/></xsl:for-each>`)
				call := "xslt_process(doc,$1::text)"
				args := []any{style}
				if arity == "three" {
					call = "xslt_process(doc,$1::text,$2::text)"
					args = append(args, "unused=1")
				}
				q := "INSERT INTO xslt_cancel_sink SELECT id," + call + " FROM xslt_cancel_source ORDER BY id"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, prepared := range []bool{false, true} {
					fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "ProcessXSLT", nil, func(ctx context.Context) fixture.RelationOutcome {
						var result sql.Result
						var err error
						if prepared {
							result, err = stmt.ExecContext(ctx, args...)
						} else {
							result, err = c.ExecContext(ctx, q, args...)
						}
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					xml2Both(t, p, b, c, fixture.Select("SELECT id,v FROM xslt_cancel_sink ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, nil))
				}
				// Reuse the same held statement and physical owner after interruption;
				// prior mutable program/tree/result work must not escape or poison it.
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE xslt_cancel_source SET doc='<r><n/><n/></r>' WHERE id=2", 1))
				h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Exec(q, 2, args...))
				xml2Both(t, p, b, c, fixture.Select("SELECT id,v FROM xslt_cancel_sink ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), ""}, {int64(2), "01"}}))
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					result, err := stmt.ExecContext(ctx, args...)
					if result != nil {
						t.Fatal("pre-canceled XSLT published result")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
					xml2Both(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM xslt_cancel_sink", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
				}
			})
		})
	}
}
