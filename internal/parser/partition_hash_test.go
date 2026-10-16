package parser

import (
	"strings"
	"testing"
)

func TestHashPartitionGrammar(t *testing.T) {
	statements, err := Parse("CREATE TABLE hash(k INT) PARTITION BY HASH(k,(k+1))")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	root := statements[0].(*CreateTable)
	if root.PartitionBy.Strategy != PartitionHash || len(root.PartitionBy.Keys) != 2 || root.Name.Parts[0].Text != "hash" {
		t.Fatal(root)
	}
	q := "CREATE TABLE child PARTITION OF hash FOR VALUES WITH(REMAINDER 2147483646,MODULUS 2147483647)"
	statements, err = Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	bound := statements[0].(*CreateTable).PartitionOf.Bound
	if bound.Strategy != PartitionHash || bound.Modulus != 2147483647 || bound.Remainder != 2147483646 || bound.Pos.Character != strings.Index(q, "WITH")+1 || len(bound.Lower) != 0 || len(bound.Upper) != 0 {
		t.Fatal(bound)
	}
	for _, options := range []string{"MODULUS -1,REMAINDER 0", "MODULUS +1,REMAINDER 0", "MODULUS 2147483648,REMAINDER 0", "MODULUS 1.5,REMAINDER 0", "MODULUS 1+1,REMAINDER 0", "MODULUS $1,REMAINDER 0", "MODULUS 2,MODULUS 4,REMAINDER 0", "MODULUS 2,REMAINDER 0,REMAINDER 1", "MODULUS 2", "REMAINDER 0", "foo 2,REMAINDER 0"} {
		if _, err := Parse("CREATE TABLE c PARTITION OF p FOR VALUES WITH(" + options + ")"); err == nil {
			t.Fatal("invalid hash syntax accepted", options)
		}
	}
	if _, err := Parse("CREATE TABLE p(k INT) PARTITION BY HASH(k) trailing"); err == nil {
		t.Fatal("valid prefix ignored trailing input")
	}
}
