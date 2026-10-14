package dbmigrate

import (
	"testing"
	"testing/fstest"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func TestApplyRejectsUnknownDirectionWithoutDatabase(t *testing.T) {
	if err := Apply("", "sideways"); err == nil {
		t.Fatal("Apply accepted an unknown migration direction")
	}
}

func TestValidateMigrationSequenceRejectsGaps(t *testing.T) {
	files := fstest.MapFS{
		"000001_first.up.sql":   {Data: []byte("SELECT 1;")},
		"000001_first.down.sql": {Data: []byte("SELECT 1;")},
		"000003_third.up.sql":   {Data: []byte("SELECT 1;")},
		"000003_third.down.sql": {Data: []byte("SELECT 1;")},
	}
	driver, err := iofs.New(files, ".")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	defer driver.Close()
	if _, err := validateMigrationSequence(driver); err == nil {
		t.Fatal("migration sequence with a missing version was accepted")
	}
}
