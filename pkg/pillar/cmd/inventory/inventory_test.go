package inventory

import "testing"

func TestCreateInventory(t *testing.T) {
	ir := inventoryReporter{}

	inventory := ir.CreateInventory()

	t.Log(inventory)
}
