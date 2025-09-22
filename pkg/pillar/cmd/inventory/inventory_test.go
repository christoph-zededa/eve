package inventory

import "testing"

func TestCreateInventory(t *testing.T) {
	ir := inventoryReporter{}

	inventory := ir.createInventory()

	t.Log(inventory)
}
