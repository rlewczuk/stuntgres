package main

import "testing"

func TestCatalogOwnersPublicWitness(t *testing.T) {
	if err := verifyCatalogOwners(); err != nil {
		t.Fatal(err)
	}
}
