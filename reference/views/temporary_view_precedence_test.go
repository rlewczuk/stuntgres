package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Independently specified PG18 phase boundaries: SELECT/alias analysis precedes
// inference; output collation follows inference; destination, duplicate-column
// and option checks follow those phases. Root pins combinations before remedies.
func TestReferenceTemporaryViewInferencePrecedence(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility(`CREATE TEMP TABLE precedence_base(id INT,a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`, nil)
		s.Exec(s.C, "INSERT INTO precedence_base VALUES(11,'a','b')", 1)
		s.Utility("CREATE TEMP VIEW precedence_replace AS SELECT 7 AS id", nil)
		s.Utility("CREATE VIEW public.precedence_public_replace AS SELECT 7 AS id", nil)
		temporaryViewNotices(t, n)
		cases := []struct {
			name, query, code, message, hint, at, notice string
		}{
			{"duplicate", "CREATE VIEW precedence_bad AS SELECT id,id FROM precedence_base", "42701", `column "id" specified more than once`, "", "", "precedence_bad"},
			{"public_duplicate", "CREATE VIEW public.precedence_bad AS SELECT id,id FROM precedence_base", "42P16", "cannot create temporary relation in non-temporary schema", "", "", "precedence_bad"},
			{"explicit_public_duplicate", "CREATE TEMP VIEW public.precedence_bad AS SELECT id,id FROM precedence_base", "42P16", "cannot create temporary relation in non-temporary schema", "", "", ""},
			{"missing_schema_duplicate", "CREATE VIEW precedence_absent.precedence_bad AS SELECT id,id FROM precedence_base", "3F000", `schema "precedence_absent" does not exist`, "", "", "precedence_bad"},
			{"replacement_duplicate", "CREATE OR REPLACE VIEW precedence_replace AS SELECT id,id FROM precedence_base", "42701", `column "id" of relation "precedence_replace" already exists`, "", "", "precedence_replace"},
			{"public_replacement_duplicate", "CREATE OR REPLACE VIEW public.precedence_public_replace AS SELECT id,id FROM precedence_base", "42P16", "cannot create temporary relation in non-temporary schema", "", "", "precedence_public_replace"},
			{"early_alias_count", "CREATE VIEW public.precedence_bad(x,y) AS SELECT id FROM precedence_base", "42601", "CREATE VIEW specifies more column names than columns", "", "", ""},
			{"early_select_column", "CREATE VIEW public.precedence_bad AS SELECT missing FROM precedence_base", "42703", `column "missing" does not exist`, "", "missing", ""},
			{"option", "CREATE VIEW precedence_bad WITH(unknown_option=true) AS SELECT id FROM precedence_base", "22023", `unrecognized parameter "unknown_option"`, "", "", "precedence_bad"},
			{"public_option", "CREATE VIEW public.precedence_bad WITH(unknown_option=true) AS SELECT id FROM precedence_base", "42P16", "cannot create temporary relation in non-temporary schema", "", "", "precedence_bad"},
			{"duplicate_option", "CREATE VIEW precedence_bad WITH(unknown_option=true) AS SELECT id,id FROM precedence_base", "22023", `unrecognized parameter "unknown_option"`, "", "", "precedence_bad"},
			{"replacement_duplicate_option", "CREATE OR REPLACE VIEW precedence_replace WITH(unknown_option=true) AS SELECT id,id FROM precedence_base", "42701", `column "id" of relation "precedence_replace" already exists`, "", "", "precedence_replace"},
			{"collation", `CREATE VIEW precedence_bad AS SELECT CASE WHEN id>0 THEN a ELSE b END AS x FROM precedence_base`, "42P22", `could not determine which collation to use for view column "x"`, "Use the COLLATE clause to set the collation explicitly.", "", "precedence_bad"},
			{"public_collation", `CREATE VIEW public.precedence_bad AS SELECT CASE WHEN id>0 THEN a ELSE b END AS x FROM precedence_base`, "42P22", `could not determine which collation to use for view column "x"`, "Use the COLLATE clause to set the collation explicitly.", "", "precedence_bad"},
			{"duplicate_collation", `CREATE VIEW precedence_bad AS SELECT CASE WHEN id>0 THEN a ELSE b END AS id,id FROM precedence_base`, "42P22", `could not determine which collation to use for view column "id"`, "Use the COLLATE clause to set the collation explicitly.", "", "precedence_bad"},
			{"option_collation", `CREATE VIEW precedence_bad WITH(unknown_option=true) AS SELECT CASE WHEN id>0 THEN a ELSE b END AS x FROM precedence_base`, "42P22", `could not determine which collation to use for view column "x"`, "Use the COLLATE clause to set the collation explicitly.", "", "precedence_bad"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				*n = nil
				if tc.notice == "" {
					defer temporaryViewNotices(t, n)
				} else {
					defer temporaryViewNotices(t, n, tc.notice)
				}
				diagnostic := fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint}
				if tc.at != "" {
					diagnostic.Position = fixture.Position(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.at)]) + 1)
				}
				step := fixture.ImplicitError(tc.query, diagnostic)
				step.Prepared = s.Prepared
				step.ErrorStage = "execute"
				fixture.RunStep(t, s.P.Ctx, s.C, s.Record, step)
				current := *s
				current.T = t
				temporaryViewSelect(&current, s.C, "SELECT id FROM precedence_base", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}})
				temporaryViewSelect(&current, s.C, "SELECT id FROM pg_temp.precedence_replace", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
				temporaryViewSelect(&current, s.C, "SELECT id FROM public.precedence_public_replace", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
				temporaryViewSelect(&current, s.C, "SELECT viewname FROM pg_catalog.pg_views WHERE viewname='precedence_bad'", []string{"viewname"}, []string{"NAME"}, [][]any{})
				temporaryViewSelect(&current, s.C, "SELECT relname FROM pg_catalog.pg_class WHERE relname='precedence_bad'", []string{"relname"}, []string{"NAME"}, [][]any{})
			})
		}
	})
}
