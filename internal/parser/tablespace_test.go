package parser

import (
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestCreateTablespaceAST(t *testing.T) {
	for _, tc := range []struct{ owner, name, identity string }{
		{"", "", ""}, {" OWNER Ordinary", "ordinary", ""}, {" OWNER CURRENT_USER", "", "current"}, {" OWNER CURRENT_ROLE", "", "current"}, {" OWNER SESSION_USER", "", "session"}, {" OWNER \"current_user\"", "current_user", ""},
	} {
		q := "CREATE TABLESPACE Mixed" + tc.owner + " LOCATION '/a''b' WITH (seq_page_cost=1,ns.random_page_cost=2)"
		stmts, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		s, ok := stmts[0].(*CreateTablespace)
		if !ok || len(stmts) != 1 {
			t.Fatal(stmts)
		}
		if s.Name.Text != "mixed" || s.Name.Pos.Character != 19 || s.Pos.Character != 1 || s.Location != "/a'b" || s.LocationPos.Character != strings.Index(q, "'/a")+1 {
			t.Fatal(s)
		}
		if tc.owner == "" {
			if s.Owner != nil {
				t.Fatal("omitted owner", s.Owner)
			}
		} else if s.Owner == nil || s.Owner.Name != tc.name || s.Owner.Identity != tc.identity || s.Owner.Pos.Character != strings.Index(q, tc.owner)+len(" OWNER ")+1 {
			t.Fatal("typed owner", s.Owner)
		}
		if len(s.Options) != 2 || s.Options[0].Name.Text != "seq_page_cost" || !s.Options[0].HasValue || s.Options[1].Namespace != "ns" || s.Options[1].Name.Text != "random_page_cost" {
			t.Fatal(s.Options)
		}
	}
	stmts, err := Parse("CREATE TABLESPACE \"PG_Name\" LOCATION '/a' ")
	if err != nil || stmts[0].(*CreateTablespace).Name.Text != "PG_Name" {
		t.Fatal(stmts, err)
	}
	if _, err := Parse("CREATE TABLE location(location TEXT); SELECT location FROM location"); err != nil {
		t.Fatal("unreserved LOCATION", err)
	}
}

func TestCreateTablespaceParseFailures(t *testing.T) {
	for _, owner := range []string{"none", "\"none\""} {
		q := "CREATE TABLESPACE t OWNER " + owner + " LOCATION 'relative'"
		stmts, err := Parse(q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42939" || len(stmts) != 0 {
			t.Fatal(d, err)
		}
	}
	for _, q := range []string{"CREATE TABLESPACE t", "CREATE TABLESPACE t LOCATION 1", "CREATE TABLESPACE t LOCATION $1", "CREATE TABLESPACE x.t LOCATION '/a'", "CREATE TABLESPACE t LOCATION '/a' WITH ()", "CREATE TABLE location(a INT); CREATE TABLESPACE t LOCATION"} {
		stmts, err := Parse(q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42601" || len(stmts) != 0 {
			t.Fatal("whole-input failure", q, stmts, err)
		}
	}
}
