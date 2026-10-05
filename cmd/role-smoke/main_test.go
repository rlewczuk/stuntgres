package main

import "testing"

func TestRoleSmoke(t *testing.T) {
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
