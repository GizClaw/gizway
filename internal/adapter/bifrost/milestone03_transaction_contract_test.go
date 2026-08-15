package bifrost

import (
	"reflect"
	"testing"
)

func TestMilestone03AdapterExposesCallerOwnedConfigTransaction(t *testing.T) {
	stores := reflect.TypeFor[*Stores]()
	for _, name := range []string{"ExecuteConfigTransaction", "CreateKeyInTransaction"} {
		if _, exists := stores.MethodByName(name); !exists {
			t.Errorf("Stores lacks %s required for atomic Provider Key, billing, and price creation", name)
		}
	}
}
