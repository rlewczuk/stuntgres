package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGIdentFileMappingsPathResolution(t *testing.T) {
	p, files := fixture.NewIdentFileScenario(t, "", map[string]string{
		"child.conf":      "child os_child db_child\n",
		"rule.conf":       "wrong os_wrong db_wrong\n",
		"other/rule.conf": "selected os_selected db_selected\n",
		"token":           "wrong_token\n",
		"other/token":     "selected_token\n",
	})
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			files.Mkdir(t, p.Ctx, b, "other/sub")
			files.Symlink(t, p.Ctx, b, "link", "other/sub")
			// These are intentionally NOT path.Join results. Released
			// AbsoluteConfigLocation preserves absolute spellings; the OS must
			// traverse link before .., choosing other/rule.conf, not rule.conf.
			root := files.Root(b)
			child := root + "/./child.conf"
			selected := root + "/link/../rule.conf"
			missing := root + "/link/../missing.conf"
			missingToken := root + "/link/../missing-token"
			files.Write(t, p.Ctx, b, "", "include {ROOT}/./child.conf\ninclude {ROOT}/link/../rule.conf\nexpanded @{ROOT}/link/../token db_expanded\ninclude {ROOT}/link/../missing.conf\nbad @{ROOT}/link/../missing-token db_bad\n")
			main := files.Path(b, "")
			want := [][]any{
				identValid(1, child, 1, "child", "os_child", "db_child"),
				identValid(2, selected, 1, "selected", "os_selected", "db_selected"),
				identValid(3, main, 3, "expanded", "selected_token", "db_expanded"),
				identInvalid(main, 4, "could not open file \""+missing+"\": No such file or directory"),
				identInvalid(main, 5, "could not open file \""+missingToken+"\": No such file or directory"),
			}
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect, want, prepared)
			}
		})
	}
}

func TestReferencePGIdentFileMappingsPhysicalLines(t *testing.T) {
	// Repeated trailing CR is removed, and a blank continuation strips the CR
	// exposed in the accumulated buffer after removing the earlier backslash.
	// Unquoted internal CR separates fields; quoting preserves an internal CR.
	p, files := fixture.NewIdentFileScenario(t, "repeated s p\r\r\nexposed s p\r\\\n\ninternal s p\r\\\nq\nquoted s \"p\rq\"\n", nil)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			main := files.Path(b, "")
			want := [][]any{
				identValid(1, main, 1, "repeated", "s", "p"),
				identValid(2, main, 2, "exposed", "s", "p"),
				identValid(3, main, 4, "internal", "s", "p"),
				identValid(4, main, 6, "quoted", "s", "p\rq"),
			}
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect+" ORDER BY map_number", want, prepared)
			}
		})
	}
}
