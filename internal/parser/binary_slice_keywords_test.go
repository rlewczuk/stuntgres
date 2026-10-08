package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestBinarySliceKeywordNameContexts(t *testing.T) {
	for _, word := range []string{"both", "leading", "trailing", "placing"} {
		for _, q := range []string{"CREATE TABLE public." + word + "(v BYTEA)", "INSERT INTO public." + word + " VALUES('abc')", "SELECT v AS " + word + " FROM public." + word, "SELECT v " + word + " FROM public." + word} {
			if _, err := Parse(q); err != nil {
				t.Fatal(q, err)
			}
		}
		if _, err := Parse("CREATE TABLE " + word + "(v BYTEA)"); err == nil {
			t.Fatal("reserved keyword became bare relation name", word)
		}
	}
}

func TestBinarySliceFunctionKeywordNames(t *testing.T) {
	for _, word := range []string{"overlay", "position", "substring", "trim"} {
		for _, name := range []string{word, "public." + word, `"` + word + `"`} {
			for _, query := range []string{
				"CREATE TABLE " + name + "(v BYTEA)",
				"INSERT INTO " + name + "(v) VALUES('abc')",
				"SELECT v AS " + word + " FROM " + name,
				"SELECT v " + word + " FROM " + name,
			} {
				if _, err := Parse(query); err != nil {
					t.Fatal(query, err)
				}
			}
		}
		for _, query := range []string{
			"CREATE TABLE t(" + word + " BYTEA)",
			"CREATE TABLE t(v " + word + ")",
			"CREATE TABLE t(v public." + word + ")",
		} {
			if _, err := Parse(query); err != nil {
				t.Fatal(query, err)
			}
		}
		for _, query := range []string{
			"CREATE TABLE t(v " + word + "(3))",
			"CREATE TABLE t(v public." + word + "(3))",
		} {
			_, err := Parse(query)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "type modifiers are unsupported" {
				t.Fatal(query, err)
			}
		}
	}
}
