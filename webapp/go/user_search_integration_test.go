package isureal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestSearchUsersPreservesUnicodeBlocksAndPaginationAcrossLargeCandidateSet(t *testing.T) {
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
	db.SetMaxOpenConns(1)
	schema := "user_search_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	if _, err := db.ExecContext(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE users (id varchar(36) PRIMARY KEY, username varchar(64) NOT NULL, secret_key_hash text NOT NULL, display_name varchar(128) NOT NULL, bio text NOT NULL, profile_image_id varchar(36), region text NOT NULL, account_type text NOT NULL, status text NOT NULL)`,
		`CREATE TABLE block_relations (id varchar(36) PRIMARY KEY, blocker_id varchar(36) NOT NULL, blocked_id varchar(36) NOT NULL)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	secretHash := strings.Repeat("0", 64)
	if _, err := db.ExecContext(ctx, `INSERT INTO users VALUES ('viewer','a-viewer',$1,'self-only','',NULL,'asia','regular','active')`, secretHash); err != nil {
		t.Fatal(err)
	}
	for i := range 400 {
		username := fmt.Sprintf("u%03d", i)
		displayName, status := "ordinary", "active"
		switch {
		case i >= 260 && i <= 312:
			displayName = "target"
		case i >= 100 && i <= 149:
			displayName = "fifty"
		case i == 350:
			displayName = "Straße"
		case i == 351:
			displayName = "e\u0301clair"
		case i == 399:
			displayName, status = "deleted-only", "deleted"
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO users VALUES ($1,$1,$2,$3,'private bio',NULL,'asia','regular',$4)`, username, secretHash, displayName, status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO block_relations VALUES ('outgoing','viewer','u260'), ('incoming','u261','viewer')`); err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	e.GET("/api/users", searchUsersHandler(&HandlerContext{DB: db}), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set("session", &Session{UserID: "viewer"})
			return next(c)
		}
	})
	server := httptest.NewServer(e)
	defer server.Close()

	search := func(query, beforeUsername, beforeID string) searchUsersResponse {
		t.Helper()
		values := url.Values{"q": {query}}
		if beforeUsername != "" {
			values.Set("beforeUsername", beforeUsername)
			values.Set("beforeId", beforeID)
		}
		response, err := server.Client().Get(server.URL + "/api/users?" + values.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("search status = %d; want 200", response.StatusCode)
		}
		var result searchUsersResponse
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	if got := len(search("missing", "", "").Items); got != 0 {
		t.Fatalf("zero-match query returned %d users; want 0", got)
	}
	if got := len(search("self-only", "", "").Items); got != 0 {
		t.Fatalf("self-only query returned %d users; want 0", got)
	}
	if got := len(search("deleted-only", "", "").Items); got != 0 {
		t.Fatalf("inactive-only query returned %d users; want 0", got)
	}
	fifty := search("fifty", "", "")
	if len(fifty.Items) != 50 || fifty.HasMore {
		t.Fatalf("50-match query returned %d users, hasMore=%v; want 50, false", len(fifty.Items), fifty.HasMore)
	}
	target := search("target", "", "")
	if len(target.Items) != 50 || !target.HasMore {
		t.Fatalf("51-match query returned %d users, hasMore=%v; want 50, true", len(target.Items), target.HasMore)
	}
	if target.Items[0].Username != "u262" || target.Items[49].Username != "u311" {
		t.Fatalf("first page usernames = %q..%q; want u262..u311", target.Items[0].Username, target.Items[49].Username)
	}
	for _, item := range target.Items {
		if item.Username == "u260" || item.Username == "u261" || item.Username == "viewer" || item.Bio != "" {
			t.Fatalf("search returned excluded or private user data: %+v", item)
		}
	}
	last := target.Items[len(target.Items)-1]
	next := search("target", last.Username, last.ID)
	if len(next.Items) != 1 || next.HasMore || next.Items[0].Username != "u312" {
		t.Fatalf("cursor page = %+v; want only u312 with hasMore=false", next)
	}
	if got := search("SS", "", "").Items; len(got) != 1 || got[0].Username != "u350" {
		t.Fatalf("case-fold query for Straße returned %+v; want u350", got)
	}
	if got := search("É", "", "").Items; len(got) != 1 || got[0].Username != "u351" {
		t.Fatalf("NFC query for decomposed display name returned %+v; want u351", got)
	}
}
