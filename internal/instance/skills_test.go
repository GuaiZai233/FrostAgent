package instance

import (
	"context"
	"os"
	"testing"

	"FrostAgent/internal/skills"
	"FrostAgent/internal/storage"
)

func TestSkillsZIPRestore(t *testing.T) {
	for _, backend := range []storage.Backend{storage.SQLite, storage.Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			config := storage.Config{Backend: backend}
			if backend == storage.Postgres {
				config.DSN = os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
				if config.DSN == "" {
					t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
				}
			}
			root := t.TempDir()
			db, err := storage.OpenWithConfig(context.Background(), root, config)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := NewDatabaseWithDB(root, db)
			if err != nil {
				db.Close()
				t.Fatal(err)
			}
			defer manager.Close()
			source, err := manager.Create("Skills Source")
			if err != nil || source.Error != "" {
				t.Fatalf("create source: %v %+v", err, source)
			}
			pkg, err := skills.ParsePackage(map[string][]byte{
				"SKILL.md":          []byte("---\nname: portable\ndescription: Portable test\n---\nExecute scripts/task.py"),
				"scripts/task.py":   []byte("#!/usr/bin/env python3\nprint('synthetic')"),
				"assets/binary.bin": {0, 1, 255},
			}, "", "scripts/task.py")
			if err != nil {
				t.Fatal(err)
			}
			pkg.Info.Enabled = true
			if err := skills.NewStore(db, source.ID).ImportPackages([]skills.Package{pkg}); err != nil {
				t.Fatal(err)
			}
			archive, err := manager.InstanceZIP(source.ID)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := manager.RestoreInstanceZIP("Skills Restored", archive)
			if err != nil {
				t.Fatal(err)
			}
			if restored.Enabled {
				t.Fatal("restored instance enabled")
			}
			actual, err := skills.NewStore(db, restored.ID).Enabled("portable")
			if err != nil || string(actual.Files["scripts/task.py"]) != "#!/usr/bin/env python3\nprint('synthetic')" || len(actual.Files["assets/binary.bin"]) != 3 || len(actual.Executables) != 1 || actual.Executables[0] != "scripts/task.py" || actual.Info.Revision != pkg.Info.Revision {
				t.Fatalf("skill resources not restored: %v", err)
			}
			if err := skills.NewStore(db, restored.ID).Delete("portable"); err != nil {
				t.Fatal(err)
			}
			if _, err := skills.NewStore(db, source.ID).Enabled("portable"); err != nil {
				t.Fatal("restored store affected source")
			}
		})
	}
}
