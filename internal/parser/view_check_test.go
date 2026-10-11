package parser

import "testing"

func TestViewCheckOptions(t *testing.T) {
	for _, tc := range []struct{ sql, mode string }{
		{"CREATE VIEW v AS SELECT a FROM t WHERE flag WITH CHECK OPTION", "cascaded"},
		{"CREATE VIEW v AS SELECT a FROM t WHERE a>0 WITH LOCAL CHECK OPTION", "local"},
		{"CREATE OR REPLACE VIEW v AS SELECT a FROM t WHERE a>0 WITH CASCADED CHECK OPTION", "cascaded"},
		{"CREATE TEMP VIEW v AS SELECT a FROM t WITH LOCAL CHECK OPTION", "local"},
		{"CREATE OR REPLACE TEMPORARY VIEW v AS SELECT a FROM t WITH CHECK OPTION", "cascaded"},
		{"CREATE VIEW v WITH(check_option=local) AS SELECT a FROM t WITH CASCADED CHECK OPTION", "cascaded"},
	} {
		statements, err := Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.sql, statements, err)
		}
		view, ok := statements[0].(*CreateView)
		if !ok || view.Query == nil || len(view.Options) == 0 {
			t.Fatal(tc.sql, statements)
		}
		option := view.Options[len(view.Options)-1]
		if option.Name.Text != "check_option" || option.Value != tc.mode || !option.HasValue {
			t.Fatal(tc, option)
		}
		if tc.sql == "CREATE VIEW v WITH(check_option=local) AS SELECT a FROM t WITH CASCADED CHECK OPTION" && len(view.Options) != 2 {
			t.Fatal("suffix overwrote duplicate")
		}
	}
	for _, sql := range []string{"CREATE VIEW v AS SELECT a FROM t WITH LOCAL CHECK", "CREATE VIEW v AS SELECT a FROM t WITH CASCADED CHECK OPTION extra", "CREATE VIEW v AS SELECT a FROM t WITH 'local' CHECK OPTION", "CREATE VIEW v AS SELECT a FROM t WITH local CHECK OPTION; SELECT FROM"} {
		if _, err := Parse(sql); err == nil {
			t.Fatal("incomplete/trailing input accepted", sql)
		}
	}
	// Statement scope reset and ordinary unreserved identifiers must survive.
	for _, sql := range []string{"CREATE VIEW v AS SELECT local,cascaded FROM t; SELECT local FROM t", "CREATE VIEW v AS SELECT a FROM t WITH CHECK OPTION; CREATE POLICY p ON t WITH CHECK(a>0)"} {
		if _, err := Parse(sql); err != nil {
			t.Fatal(sql, err)
		}
	}
}
