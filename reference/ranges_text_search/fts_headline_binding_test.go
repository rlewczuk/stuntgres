package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var ftsHeadlineForms = [][]string{
	{"text", "tsquery"}, {"text", "tsquery", "text"},
	{"regconfig", "text", "tsquery"}, {"regconfig", "text", "tsquery", "text"},
}

func ftsHeadlineArguments(types []string) []any {
	args := make([]any, len(types))
	for i, typ := range types {
		switch typ {
		case "regconfig":
			args[i] = "english"
		case "tsquery":
			args[i] = "cat"
		case "text":
			args[i] = "Cat rat"
			if i == len(types)-1 {
				args[i] = "HighlightAll=true"
			}
		}
	}
	return args
}

func TestReferenceFTSHeadlineNullMetadata(t *testing.T) {
	for form, types := range ftsHeadlineForms {
		t.Run(fmt.Sprint(form), func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer ftsConvertSetting(t, p, c)()
				for _, alias := range []string{"", " AS excerpt"} {
					label := "ts_headline"
					if alias != "" {
						label = "excerpt"
					}
					query := "SELECT ts_headline(" + ftsRankParameters(types) + ")" + alias
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer stmt.Close()
						args := ftsHeadlineArguments(types)
						for slot := -1; slot < len(args); slot++ {
							input := append([]any(nil), args...)
							var want any = "<b>Cat</b> rat"
							if slot >= 0 {
								input[slot], want = nil, nil
							}
							ftsRankRoutes(t, p, b, c, query, label, "TEXT", want, input...)
							rows, err := stmt.QueryContext(p.Ctx, input...)
							if err != nil {
								t.Fatal(err)
							}
							ftsRankRead(t, p, b, rows, label, "TEXT", want, false)
						}
						docIndex := 0
						if types[0] == "regconfig" {
							docIndex = 1
						}
						for _, document := range []string{"", "rat", "Cat rat"} {
							input := append([]any(nil), args...)
							input[docIndex] = document
							want := document
							if document == "Cat rat" {
								want = "<b>Cat</b> rat"
							}
							ftsRankRoutes(t, p, b, c, query, label, "TEXT", want, input...)
							rows, err := stmt.QueryContext(p.Ctx, input...)
							if err != nil {
								t.Fatal(err)
							}
							ftsRankRead(t, p, b, rows, label, "TEXT", want, false)
						}
					}()
					zero := query + " WHERE false"
					args := ftsHeadlineArguments(types)
					rows, err := c.QueryContext(p.Ctx, zero, args...)
					if err != nil {
						t.Fatal(err)
					}
					ftsRankRead(t, p, b, rows, label, "TEXT", nil, true)
					z, err := c.PrepareContext(p.Ctx, zero)
					if err != nil {
						t.Fatal(err)
					}
					for range 2 {
						rows, err := z.QueryContext(p.Ctx, args...)
						if err != nil {
							z.Close()
							t.Fatal(err)
						}
						ftsRankRead(t, p, b, rows, label, "TEXT", nil, true)
					}
					z.Close()
				}
				ftsRankRoutes(t, p, b, c, "SELECT ts_headline('english'::regconfig,NULL::text,'cat'::tsquery,'StartSel=')", "ts_headline", "TEXT", nil)
				ftsRankRoutes(t, p, b, c, "SELECT ts_headline(NULL::regconfig,'Cat'::text,'cat'::tsquery,'StartSel=')", "ts_headline", "TEXT", nil)
				for _, typ := range []string{"text", "tsquery", "regconfig"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN headline_"+typ+" AS "+typ, 0))
				}
				ftsRankRoutes(t, p, b, c, "SELECT ts_headline('english'::headline_regconfig,'Cat'::headline_text,'cat'::headline_tsquery)", "ts_headline", "TEXT", "<b>Cat</b>")
			})
		})
	}
}
