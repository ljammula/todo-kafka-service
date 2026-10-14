package dbmigrate

import "testing"

func TestApplyRejectsUnknownDirectionWithoutDatabase(t *testing.T) {
	if err := Apply("", "sideways"); err == nil {
		t.Fatal("Apply accepted an unknown migration direction")
	}
}
