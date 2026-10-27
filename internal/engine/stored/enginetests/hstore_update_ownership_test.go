package enginetests_test

import "testing"

func TestHstoreUpdateStoredOwnership(t *testing.T) {
 t.Run("reconstruction",func(t *testing.T){testHstoreSelectedStoredReconstruction(t,"update")})
 t.Run("remap",func(t *testing.T){testHstoreSelectedStoredRemap(t,"update")})
}
