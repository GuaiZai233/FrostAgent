package storage

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestPostgresMigrationSessionImagesRoundTripQuotaAndCascade(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			_, _, db := compatibilityDatabase(t, backend)
			populateMigrationFixture(t, db)
			ctx := context.Background()
			const instance = "synthetic-instance"
			first := bytes.Repeat([]byte{1}, 400)
			second := bytes.Repeat([]byte{2}, 400)
			third := bytes.Repeat([]byte{3}, 400)

			if err := db.PutSessionImage(ctx, instance, "first", "image/png", first, 1000); err != nil {
				t.Fatal(err)
			}
			time.Sleep(2 * time.Millisecond)
			if err := db.PutSessionImage(ctx, instance, "second", "image/png", second, 1000); err != nil {
				t.Fatal(err)
			}
			time.Sleep(2 * time.Millisecond)
			// Viewing the first image makes the second the least recently used.
			if image, ok, err := db.GetSessionImage(ctx, instance, "first"); err != nil || !ok || !bytes.Equal(image.Data, first) || image.ContentType != "image/png" {
				t.Fatalf("round trip failed: ok=%v err=%v", ok, err)
			}
			time.Sleep(2 * time.Millisecond)
			if err := db.PutSessionImage(ctx, instance, "third", "image/png", third, 1000); err != nil {
				t.Fatal(err)
			}
			if ok, _ := db.HasSessionImage(ctx, instance, "second"); ok {
				t.Fatal("least recently used image survived the quota")
			}
			for _, key := range []string{"first", "third"} {
				if ok, err := db.HasSessionImage(ctx, instance, key); err != nil || !ok {
					t.Fatalf("%s evicted too eagerly: %v", key, err)
				}
			}
			var total int64
			if err := db.SQL.QueryRow(db.Bind(`SELECT COALESCE(SUM(size),0) FROM session_images WHERE instance_id = ?`), instance).Scan(&total); err != nil || total > 1000 {
				t.Fatalf("quota exceeded: %d %v", total, err)
			}
			if _, ok, _ := db.GetSessionImage(ctx, "other-instance", "first"); ok {
				t.Fatal("image visible to another instance")
			}

			compatibilityExec(t, db, `DELETE FROM instances WHERE id='synthetic-instance'`)
			var count int
			if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM session_images`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("images did not cascade with the instance: %d %v", count, err)
			}
		})
	}
}
