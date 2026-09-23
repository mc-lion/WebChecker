package db

import (
	"io/fs"
	"strings"
	"testing"

	"webchecker/migrations"
)

func TestSplitSQLKeepsMultilineStatements(t *testing.T) {
	script := `
-- комментарий
SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.columns
    WHERE table_name = 'monitors'
);
SET @ddl := IF(@col_exists = 0, 'ALTER TABLE monitors ADD COLUMN x INT', 'DO 0');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
`
	stmts := splitSQL(script)
	if len(stmts) != 5 {
		t.Fatalf("expected 5 statements, got %d: %#v", len(stmts), stmts)
	}
	if !strings.HasPrefix(stmts[0], "SET @col_exists") || !strings.Contains(stmts[0], "information_schema.columns") {
		t.Fatalf("multiline SET was split: %q", stmts[0])
	}
	for _, stmt := range stmts {
		if strings.HasSuffix(stmt, ";") {
			t.Fatalf("statement keeps trailing semicolon: %q", stmt)
		}
	}
}

func TestEmbeddedMigrationsParse(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		stmts := splitSQL(string(raw))
		if len(stmts) == 0 {
			t.Fatalf("%s produced no statements", entry.Name())
		}
		for _, stmt := range stmts {
			if strings.TrimSpace(stmt) == "" {
				t.Fatalf("%s produced an empty statement", entry.Name())
			}
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("no migrations embedded")
	}
}
