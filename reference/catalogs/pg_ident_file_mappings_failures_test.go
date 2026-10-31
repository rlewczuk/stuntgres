package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGIdentFileMappingsIncludeFailures(t *testing.T) {
	p, files := fixture.NewIdentFileScenario(t, "before s p\ninclude_dir {ROOT}/failures\nafter s p\n", map[string]string{
		"failures/A_bad.conf":  "unreadable_a s p\n",
		"failures/B_good.conf": "included os_included db_included\n",
		"failures/C_bad.conf":  "unreadable_c s p\n",
	})
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// Discovery/stat succeeds for all three real files. Only opening
			// A/C fails: this exercises aggregation, not dangling-link discovery.
			for _, name := range []string{"failures/A_bad.conf", "failures/C_bad.conf"} {
				files.Chmod(t, p.Ctx, b, name, 0)
			}
			main := files.Path(b, "")
			deniedA := "could not open file \"" + files.Path(b, "failures/A_bad.conf") + "\": Permission denied"
			deniedC := "could not open file \"" + files.Path(b, "failures/C_bad.conf") + "\": Permission denied"
			// The released loop retains err_msg on a successful open, so
			// B_good contributes the previous A_bad diagnostic a second time.
			want := [][]any{
				identValid(1, main, 1, "before", "s", "p"),
				identValid(2, files.Path(b, "failures/B_good.conf"), 1, "included", "os_included", "db_included"),
				identInvalid(main, 2, deniedA+"\n"+deniedA+"\n"+deniedC),
				identValid(3, main, 3, "after", "s", "p"),
			}
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect, want, prepared)
			}

			files.Write(t, p.Ctx, b, "", "before s p\ninclude_if_exists {ROOT}/failures/A_bad.conf\nafter s p\n")
			want = [][]any{
				identValid(1, main, 1, "before", "s", "p"),
				identInvalid(main, 2, deniedA),
				identValid(2, main, 3, "after", "s", "p"),
			}
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect+" ORDER BY line_number", want, prepared)
			}

			// All optional opens share the ordinary include depth. Depth ten
			// succeeds; attempted eleven is rejected even when its target is absent.
			for i := 1; i <= 10; i++ {
				data := "deep os_deep db_deep\n"
				if i < 10 {
					data = fmt.Sprintf("include_if_exists f%d\n", i+1)
				}
				files.Write(t, p.Ctx, b, fmt.Sprintf("optional/f%d", i), data)
			}
			files.Write(t, p.Ctx, b, "", "include_if_exists {ROOT}/optional/f1\n")
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect, [][]any{
					identValid(1, files.Path(b, "optional/f10"), 1, "deep", "os_deep", "db_deep"),
				}, prepared)
			}
			files.Write(t, p.Ctx, b, "optional/f10", "include_if_exists absent11\n")
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect, [][]any{
					identInvalid(files.Path(b, "optional/f10"), 1, "could not open file \""+files.Path(b, "optional/absent11")+"\": maximum nesting depth exceeded"),
				}, prepared)
			}
			// ENOENT alone remains optional at the allowed depth-ten boundary.
			files.Write(t, p.Ctx, b, "optional/f9", "include_if_exists absent10\n")
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect, nil, prepared)
			}
		})
	}
}
