package isureal

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestLoadMemoryInteractionsLimitsEachPostAndKeepsContractOrder(t *testing.T) {
	dsn := os.Getenv("ISUREAL_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_TEST_DATABASE_DSN is not set")
	}
	ctx := context.Background()
	db, err := sqlx.ConnectContext(ctx, "pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := "memory_interactions_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE users (id varchar(36) PRIMARY KEY, username varchar(64) NOT NULL, secret_key_hash text NOT NULL, display_name varchar(128) NOT NULL, bio text NOT NULL, profile_image_id varchar(36), region text NOT NULL, account_type text NOT NULL, status text NOT NULL)`,
		`CREATE TABLE realmojis (id varchar(36) PRIMARY KEY, post_id varchar(36) NOT NULL, reactor_id varchar(36) NOT NULL, image_id varchar(36) NOT NULL, reaction_type text NOT NULL, created_at timestamptz NOT NULL, replaced_at timestamptz, deleted_at timestamptz)`,
		`CREATE TABLE comments (id varchar(36) PRIMARY KEY, post_id varchar(36) NOT NULL, author_id varchar(36) NOT NULL, body text NOT NULL, created_at timestamptz NOT NULL, deleted_at timestamptz)`,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users VALUES ('actor','actor',$1,'Actor','',NULL,'asia','regular','active'), ('inactive','inactive',$1,'Inactive','',NULL,'asia','regular','deleted')`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	items := []memoryPostResponse{{createPostResponse: createPostResponse{ID: "post-a"}}, {createPostResponse: createPostResponse{ID: "post-b"}}}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, item := range items {
		for i := range 55 {
			id := fmt.Sprintf("%s-r-%02d", item.ID, i)
			createdAt := base.Add(time.Duration(i) * time.Second)
			if _, err := tx.ExecContext(ctx, `INSERT INTO realmojis VALUES ($1,$2,'actor','image','like',$3,NULL,NULL)`, id, item.ID, createdAt); err != nil {
				t.Fatal(err)
			}
			id = fmt.Sprintf("%s-c-%02d", item.ID, i)
			if _, err := tx.ExecContext(ctx, `INSERT INTO comments VALUES ($1,$2,'actor',$4,$3,NULL)`, id, item.ID, createdAt, id); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
  INSERT INTO realmojis VALUES
    ($1,$3,'inactive','image','like',$4,NULL,NULL),
    ($2,$3,'actor','image','like',$4,NULL,$4),
    ($5,$3,'actor','image','like',$4,$4,NULL)`, item.ID+"-inactive-r", item.ID+"-deleted-r", item.ID, base.Add(100*time.Second), item.ID+"-replaced-r"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `
  INSERT INTO comments VALUES
    ($1,$3,'inactive',$5,$4,NULL),
    ($2,$3,'actor',$6,$4,$4)`, item.ID+"-inactive-c", item.ID+"-deleted-c", item.ID, base.Add(-time.Second), "inactive", "deleted"); err != nil {
			t.Fatal(err)
		}
	}
	if err := loadMemoryInteractions(ctx, tx, items, newProfileImageURLSigner()); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if len(item.RealMojis) != 50 || len(item.Comments) != 50 {
			t.Fatalf("post %s interaction counts = %d RealMojis, %d comments; want 50 each", item.ID, len(item.RealMojis), len(item.Comments))
		}
		if got := item.RealMojis[0].ID; got != item.ID+"-r-54" {
			t.Errorf("post %s newest RealMoji = %q; want %q", item.ID, got, item.ID+"-r-54")
		}
		if got := item.RealMojis[49].ID; got != item.ID+"-r-05" {
			t.Errorf("post %s oldest returned RealMoji = %q; want %q", item.ID, got, item.ID+"-r-05")
		}
		if got := item.Comments[0].ID; got != item.ID+"-c-00" {
			t.Errorf("post %s earliest comment = %q; want %q", item.ID, got, item.ID+"-c-00")
		}
		if got := item.Comments[49].ID; got != item.ID+"-c-49" {
			t.Errorf("post %s latest returned comment = %q; want %q", item.ID, got, item.ID+"-c-49")
		}
	}
}
