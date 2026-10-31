package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGIdentFileMappingsIncludes(t *testing.T) {
	manifest := map[string]string{
		"sub/child.conf": "# child comment\nchild os_child db_child\ninclude leaf.conf\n",
		"sub/leaf.conf":  "leaf os_leaf db_leaf\n",
		"conf.d/A.conf":  "A s p\n", "conf.d/a.conf": "a s p\n", "conf.d/b.conf": "b s p\n",
		"conf.d/.hidden.conf": "ignored s p\n", "conf.d/ignored.txt": "ignored s p\n",
		"single token": "expanded\n", "many": "one two\n", "nested": "include single.conf\n", "single.conf": "nested_value\n",
	}
	p, files := fixture.NewIdentFileScenario(t, "first os_first db_first\ninclude {ROOT}/sub/child.conf\ninclude_if_exists {ROOT}/absent.conf\ninclude_dir {ROOT}/conf.d\nlast os_last db_last\n", manifest)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			files.Mkdir(t, p.Ctx, b, "conf.d/z.conf")
			main := files.Path(b, "")
			want := [][]any{
				identValid(1, main, 1, "first", "os_first", "db_first"),
				identValid(2, files.Path(b, "sub/child.conf"), 2, "child", "os_child", "db_child"),
				identValid(3, files.Path(b, "sub/leaf.conf"), 1, "leaf", "os_leaf", "db_leaf"),
				identValid(4, files.Path(b, "conf.d/A.conf"), 1, "A", "s", "p"), identValid(5, files.Path(b, "conf.d/a.conf"), 1, "a", "s", "p"), identValid(6, files.Path(b, "conf.d/b.conf"), 1, "b", "s", "p"),
				identValid(7, main, 5, "last", "os_last", "db_last"),
			}
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect+" ORDER BY map_number", want, prepared)
				identRead(t, p, b, b.DB, identSelect, want, prepared)
			}
			files.Symlink(t, p.Ctx, b, "conf.d/c.conf", "../sub/leaf.conf")
			files.Write(t, p.Ctx, b, "", "\"include_dir\",ignored {ROOT}/conf.d,ignored\n")
			identRead(t, p, b, b.DB, identSelect+" ORDER BY map_number", [][]any{
				identValid(1, files.Path(b, "conf.d/A.conf"), 1, "A", "s", "p"),
				identValid(2, files.Path(b, "conf.d/a.conf"), 1, "a", "s", "p"),
				identValid(3, files.Path(b, "conf.d/b.conf"), 1, "b", "s", "p"),
				identValid(4, files.Path(b, "conf.d/c.conf"), 1, "leaf", "os_leaf", "db_leaf"),
			}, true)
			lexical := "\"map name\" \"os, #name\" \"db\"\"name\"\r\nempty s \"\"\nunclosed s \"db name\nfragments os\" user\"name db\nliteral \"@none\" p\nexpanded @\"{ROOT}/single token\" p\nordinary os\\name db\\1\ncontinued os\\\nname db\n# continued comment\\\nnot_a_rule s p\nare /(?=abc)abc /(?i)xyz\nauth all +unknown\ninclude user role\ntrailing s p ignored,values\n"
			files.Write(t, p.Ctx, b, "", lexical)
			want = [][]any{
				identValid(1, main, 1, "map name", "os, #name", "db\"name"), identValid(2, main, 2, "empty", "s", ""), identValid(3, main, 3, "unclosed", "s", "db name"), identValid(4, main, 4, "fragments", "os username", "db"),
				identValid(5, main, 5, "literal", "@none", "p"), identValid(6, main, 6, "expanded", "expanded", "p"), identValid(7, main, 7, "ordinary", "os\\name", "db\\1"), identValid(8, main, 8, "continued", "osname", "db"),
				identValid(9, main, 12, "are", "/(?=abc)abc", "/(?i)xyz"), identValid(10, main, 13, "auth", "all", "+unknown"), identValid(11, main, 14, "include", "user", "role"), identValid(12, main, 15, "trailing", "s", "p"),
			}
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect+" ORDER BY map_number", want, prepared)
			}
			// REL_18_6 hba.c's progress guard retains the remaining backslash
			// when the next physical line is blank, but strips a new backslash
			// that advances the accumulated length. Assert both actual files.
			files.Write(t, p.Ctx, b, "", "guard s p\\\\\n\nafter s p\n")
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect+" ORDER BY map_number", [][]any{
					identValid(1, main, 1, "guard", "s", "p\\"),
					identValid(2, main, 3, "after", "s", "p"),
				}, prepared)
			}
			files.Write(t, p.Ctx, b, "", "successive s p\\\n\\\n")
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect, [][]any{
					identValid(1, main, 1, "successive", "s", "p"),
				}, prepared)
			}
			files.Write(t, p.Ctx, b, "", "nested @{ROOT}/nested p\nmany @{ROOT}/many p\n")
			identRead(t, p, b, b.DB, identSelect+" ORDER BY line_number", [][]any{identValid(1, main, 1, "nested", "nested_value", "p"), identInvalid(main, 2, identMultiple)}, false)
			// Shared include/@ depth: exactly ten nested opens succeed; eleven fails
			// at the referring record, not an invented cycle diagnostic.
			for i := range 11 {
				data := "deep s p\n"
				if i < 10 {
					data = fmt.Sprintf("include f%d\n", i+1)
				}
				files.Write(t, p.Ctx, b, fmt.Sprintf("f%d", i), data)
			}
			// Top is depth zero; f1 through f10 are ten includes.
			files.Write(t, p.Ctx, b, "", "include {ROOT}/f1\n")
			identRead(t, p, b, b.DB, identSelect, [][]any{identValid(1, files.Path(b, "f10"), 1, "deep", "s", "p")}, false)
			files.Write(t, p.Ctx, b, "f10", "include f11\n")
			identRead(t, p, b, b.DB, identSelect, [][]any{identInvalid(files.Path(b, "f10"), 1, "could not open file \""+files.Path(b, "f11")+"\": maximum nesting depth exceeded")}, true)
			// @ expansion shares exactly the same depth counter with include directives.
			files.Write(t, p.Ctx, b, "f1", "mixed @f2 p\n")
			files.Write(t, p.Ctx, b, "f10", "deep_token\n")
			identRead(t, p, b, b.DB, identSelect, [][]any{identValid(1, files.Path(b, "f1"), 1, "mixed", "deep_token", "p")}, true)
			files.Write(t, p.Ctx, b, "f10", "@f11\n")
			identRead(t, p, b, b.DB, identSelect, [][]any{identInvalid(files.Path(b, "f1"), 1, "could not open file \""+files.Path(b, "f11")+"\": maximum nesting depth exceeded")}, false)
			files.Write(t, p.Ctx, b, "cycle", "include cycle\n")
			files.Write(t, p.Ctx, b, "", "include {ROOT}/cycle\n")
			identRead(t, p, b, b.DB, identSelect, [][]any{identInvalid(files.Path(b, "cycle"), 1, "could not open file \""+files.Path(b, "cycle")+"\": maximum nesting depth exceeded")}, true)
			files.Write(t, p.Ctx, b, "", "first s p\ninclude {ROOT}/missing\ninclude {ROOT}/sub/leaf.conf\nlast s p\n")
			identRead(t, p, b, b.DB, identSelect, [][]any{
				identValid(1, main, 1, "first", "s", "p"),
				identInvalid(main, 2, "could not open file \""+files.Path(b, "missing")+"\": No such file or directory"),
				identValid(2, files.Path(b, "sub/leaf.conf"), 1, "leaf", "os_leaf", "db_leaf"),
				identValid(3, main, 4, "last", "s", "p"),
			}, true)
			// Directory discovery fails as a unit on a dangling symlink; none of the
			// otherwise valid selected files is parsed before discovery finishes.
			files.Symlink(t, p.Ctx, b, "conf.d/d.conf", "../missing")
			files.Write(t, p.Ctx, b, "", "include_dir {ROOT}/conf.d\n")
			identRead(t, p, b, b.DB, identSelect, [][]any{identInvalid(main, 1, "could not stat file \""+files.Path(b, "conf.d/d.conf")+"\"")}, false)
			files.Write(t, p.Ctx, b, "", "include_if_exists {ROOT}/absent\ninclude_dir {ROOT}/empty\n")
			files.Mkdir(t, p.Ctx, b, "empty")
			identRead(t, p, b, b.DB, identSelect, nil, false)
		})
	}
}

func TestReferencePGIdentFileMappingsDiagnostics(t *testing.T) {
	p, files := fixture.NewIdentFileScenario(t, "# empty\n", nil)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			main := files.Path(b, "")
			for _, data := range []string{"", "# only comment\n\n", "include_if_exists {ROOT}/absent\n"} {
				files.Write(t, p.Ctx, b, "", data)
				for _, prepared := range []bool{false, true} {
					identRead(t, p, b, b.DB, identSelect, nil, prepared)
				}
			}
			files.Write(t, p.Ctx, b, "", "missing\nmap s\nm x,y /[\nm s /[\ninclude {ROOT}/missing\nmap s p @{ROOT}/trailing_missing\ninclude_dir {ROOT}/absent_dir\ninclude_dir \"\"\n")
			want := [][]any{identInvalid(main, 1, identMissing), identInvalid(main, 2, identMissing), identInvalid(main, 3, identMultiple), identInvalid(main, 4, identRegex), identInvalid(main, 5, "could not open file \""+files.Path(b, "missing")+"\": No such file or directory"), identInvalid(main, 6, "could not open file \""+files.Path(b, "trailing_missing")+"\": No such file or directory"), identInvalid(main, 7, "could not open directory \""+files.Path(b, "absent_dir")+"\""), identInvalid(main, 8, "empty configuration directory name")}
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect+" ORDER BY line_number", want, prepared)
			}
			files.Remove(t, p.Ctx, b, "")
			// Description/Prepare is not file capture and must succeed even when missing.
			stmt, err := b.DB.PrepareContext(p.Ctx, identSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: identSelect}
			for _, e := range []fixture.Executor{b.DB, held} {
				s := fixture.ImplicitError(identSelect, fixture.Diagnostic{Code: "58P01", Message: "could not open file \"" + main + "\": No such file or directory"})
				s.SelectRows = true
				s.Prepared = true
				fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
			}
			files.Write(t, p.Ctx, b, "", "recovered s p\n")
			identRead(t, p, b, b.DB, identSelect, [][]any{identValid(1, main, 1, "recovered", "s", "p")}, true)
		})
	}
}
