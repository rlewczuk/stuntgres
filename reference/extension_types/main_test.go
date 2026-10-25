package reference_test

import (
	"os"
	"stuntgres/reference/fixture"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(fixture.Main(m)) }
