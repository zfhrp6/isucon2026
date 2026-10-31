package isureal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type testHTTPResult struct {
	response *http.Response
	err      error
}

func TestRegisterHandlerEnforcesUniqueUsernamesConcurrently(t *testing.T) {
	db, server := newUserRegistrationTestServer(t, true)
	defer server.Close()

	const requests = 16
	results := make(chan testHTTPResult, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := sendJSON(server.Client(), http.MethodPost, server.URL+"/register", registerRequest{
				Username: "same-name", DisplayName: "Same", Region: "asia",
			})
			results <- testHTTPResult{response: response, err: err}
		}()
	}
	wg.Wait()
	close(results)

	created, conflicts := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("registration request failed: %v", result.err)
		}
		status := result.response.StatusCode
		result.response.Body.Close()
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("registration status = %d; want 201 or 409", status)
		}
	}
	if created != 1 || conflicts != requests-1 {
		t.Fatalf("registration outcomes = %d created, %d conflicts; want 1 created and %d conflicts", created, conflicts, requests-1)
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM users WHERE username = 'same-name'`); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stored matching users = %d; want 1", count)
	}
}

func TestRegisterHandlerPreservesUserAndLoginContract(t *testing.T) {
	db, server := newUserRegistrationTestServer(t, true)
	defer server.Close()

	response := postJSON(t, server.Client(), server.URL+"/register", registerRequest{
		Username: "new-user", DisplayName: "New User", Region: "europe",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("registration status = %d; want 201", response.StatusCode)
	}
	var created registerResponse
	decodeResponse(t, response, &created)
	if created.SecretKey == "" || created.User.Username != "new-user" || created.User.DisplayName != "New User" || created.User.Region != "europe" || created.User.Bio != "" || created.User.AccountType != "regular" || created.User.Status != "active" {
		t.Fatalf("registration response did not preserve initialized user fields: %+v", created.User)
	}

	response = postJSON(t, server.Client(), server.URL+"/login", loginRequest{
		Username: "new-user", SecretKey: created.SecretKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d; want 200", response.StatusCode)
	}
	var loggedIn loginResponse
	decodeResponse(t, response, &loggedIn)
	if loggedIn.Token == "" || loggedIn.User.ID != created.User.ID {
		t.Fatalf("login response does not identify the registered user")
	}

	if _, err := db.Exec(`UPDATE users SET status = 'deleted' WHERE username = 'new-user'`); err != nil {
		t.Fatal(err)
	}
	response = postJSON(t, server.Client(), server.URL+"/register", registerRequest{
		Username: "new-user", DisplayName: "Reused", Region: "asia",
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("registration with a deleted username status = %d; want 409", response.StatusCode)
	}
	var apiError APIErrorResponse
	decodeResponse(t, response, &apiError)
	if apiError.Error != "username_taken" {
		t.Fatalf("duplicate username error = %q; want username_taken", apiError.Error)
	}
}

func TestRegisterFailsUntilUsernameMigrationIsApplied(t *testing.T) {
	db, server := newUserRegistrationTestServer(t, false)
	defer server.Close()

	response := postJSON(t, server.Client(), server.URL+"/register", registerRequest{
		Username: "migration-required", DisplayName: "Migration", Region: "asia",
	})
	defer response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("registration without the unique index status = %d; want 500", response.StatusCode)
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM users WHERE username = 'migration-required'`); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("registration without the unique index created %d users; want 0", count)
	}
}

func TestRegisterDoesNotWaitForAnotherUsersInsert(t *testing.T) {
	db, server := newUserRegistrationTestServer(t, true)
	defer server.Close()

	blocker, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.Exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, 'uncommitted', $2, 'Held', 'asia')`, uuid.NewString(), strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}

	responseCh := make(chan *http.Response, 1)
	go func() {
		response, err := sendJSON(server.Client(), http.MethodPost, server.URL+"/register", registerRequest{
			Username: "independent", DisplayName: "Independent", Region: "asia",
		})
		if err != nil {
			responseCh <- nil
			return
		}
		responseCh <- response
	}()

	select {
	case response := <-responseCh:
		if response == nil {
			t.Fatal("independent registration request failed")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("independent registration status = %d; want 201", response.StatusCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("independent registration did not finish while another user insert remained uncommitted")
	}

	var stillUncommitted string
	if err := blocker.Get(&stillUncommitted, `SELECT username FROM users WHERE username = 'uncommitted'`); err != nil {
		t.Fatalf("blocker transaction lost its uncommitted row: %v", err)
	}
	var visible bool
	if err := db.Get(&visible, `SELECT EXISTS(SELECT 1 FROM users WHERE username = 'uncommitted')`); err != nil {
		t.Fatal(err)
	}
	if visible {
		t.Fatal("independent registration completed only after the blocker row became visible")
	}
}

func TestPostingWindowWaitsForRegistrationAndNotifiesCommittedUser(t *testing.T) {
	db, server := newUserRegistrationTestServer(t, true)
	defer server.Close()

	registration, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer registration.Rollback()
	if _, err := registration.Exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, 'arriving', $2, 'Arriving', 'asia')`, uuid.NewString(), strings.Repeat("0", 64)); err != nil {
		registration.Rollback()
		t.Fatal(err)
	}

	windowResponse := make(chan *http.Response, 1)
	go func() {
		response, err := sendJSON(server.Client(), http.MethodPost, server.URL+"/posting-window", openPostingWindowRequest{
			Region: "asia", LocalDate: "2030-01-01",
		})
		if err != nil {
			windowResponse <- nil
			return
		}
		windowResponse <- response
	}()
	waitForTableLock(t, db, "ShareRowExclusiveLock", false)

	if err := registration.Commit(); err != nil {
		t.Fatal(err)
	}
	response := receiveHTTPResponse(t, windowResponse)
	if response == nil {
		t.Fatal("posting-window request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("posting-window status = %d; want 201", response.StatusCode)
	}
	var window openPostingWindowResponse
	decodeResponse(t, response, &window)
	var notified bool
	if err := db.Get(&notified, `SELECT EXISTS(SELECT 1 FROM notifications WHERE user_id = (SELECT id FROM users WHERE username = 'arriving') AND posting_window_id = $1)`, window.ID); err != nil {
		t.Fatal(err)
	}
	if !notified {
		t.Fatal("posting window did not notify the user committed before its user snapshot")
	}
}

func TestRegistrationWaitsForPostingWindowSnapshot(t *testing.T) {
	db, server := newUserRegistrationTestServer(t, true)
	defer server.Close()

	const advisoryKey int64 = 94721583
	advisoryLock, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer advisoryLock.Rollback()
	if _, err := advisoryLock.Exec(`SELECT pg_advisory_xact_lock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`
		CREATE FUNCTION hold_window_notification() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(%d);
			RETURN NEW;
		END;
		$$
	`, advisoryKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER hold_window_notification BEFORE INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION hold_window_notification()`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, 'existing', $2, 'Existing', 'asia')`, uuid.NewString(), strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}

	windowResponse := make(chan *http.Response, 1)
	go func() {
		response, err := sendJSON(server.Client(), http.MethodPost, server.URL+"/posting-window", openPostingWindowRequest{
			Region: "asia", LocalDate: "2030-01-02",
		})
		if err != nil {
			windowResponse <- nil
			return
		}
		windowResponse <- response
	}()
	waitForTableLock(t, db, "ShareRowExclusiveLock", true)

	registrationResponse := make(chan *http.Response, 1)
	go func() {
		response, err := sendJSON(server.Client(), http.MethodPost, server.URL+"/register", registerRequest{
			Username: "after-window", DisplayName: "After", Region: "asia",
		})
		if err != nil {
			registrationResponse <- nil
			return
		}
		registrationResponse <- response
	}()
	waitForTableLock(t, db, "RowExclusiveLock", false)

	if err := advisoryLock.Commit(); err != nil {
		t.Fatal(err)
	}
	window := receiveHTTPResponse(t, windowResponse)
	if window == nil {
		t.Fatal("posting-window request failed")
	}
	defer window.Body.Close()
	if window.StatusCode != http.StatusCreated {
		t.Fatalf("posting-window status = %d; want 201", window.StatusCode)
	}
	var opened openPostingWindowResponse
	decodeResponse(t, window, &opened)

	registered := receiveHTTPResponse(t, registrationResponse)
	if registered == nil {
		t.Fatal("registration request failed")
	}
	defer registered.Body.Close()
	if registered.StatusCode != http.StatusCreated {
		t.Fatalf("registration status = %d; want 201", registered.StatusCode)
	}
	var notified bool
	if err := db.Get(&notified, `SELECT EXISTS(SELECT 1 FROM notifications WHERE user_id = (SELECT id FROM users WHERE username = 'after-window') AND posting_window_id = $1)`, opened.ID); err != nil {
		t.Fatal(err)
	}
	if notified {
		t.Fatal("registration committed after the posting-window user snapshot but was added to its notifications")
	}
}

func TestRegionUpdateAndPostingWindowUseOneUserSnapshot(t *testing.T) {
	t.Run("region update commits before window snapshot", func(t *testing.T) {
		db, server := newUserRegistrationTestServer(t, true)
		defer server.Close()
		userID := insertTestUser(t, db, "update-first", "europe")

		const advisoryKey int64 = 94721584
		advisoryLock := holdTestAdvisoryLock(t, db, advisoryKey)
		defer advisoryLock.Rollback()
		if _, err := db.Exec(fmt.Sprintf(`
			CREATE FUNCTION hold_user_update() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN
				PERFORM pg_advisory_xact_lock(%d);
				RETURN NEW;
			END;
			$$
		`, advisoryKey)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TRIGGER hold_user_update BEFORE UPDATE OF region ON users FOR EACH ROW EXECUTE FUNCTION hold_user_update()`); err != nil {
			t.Fatal(err)
		}

		updateResponse := make(chan *http.Response, 1)
		go func() {
			response, err := sendJSONForUser(server.Client(), server.URL+"/me", userID, updateMeRequest{Region: registrationRegionPointer("asia")})
			if err != nil {
				updateResponse <- nil
				return
			}
			updateResponse <- response
		}()
		waitForTableLock(t, db, "RowExclusiveLock", true)

		windowResponse := make(chan *http.Response, 1)
		go func() {
			response, err := sendJSON(server.Client(), http.MethodPost, server.URL+"/posting-window", openPostingWindowRequest{Region: "asia", LocalDate: "2030-02-01"})
			if err != nil {
				windowResponse <- nil
				return
			}
			windowResponse <- response
		}()
		waitForTableLock(t, db, "ShareRowExclusiveLock", false)

		if err := advisoryLock.Commit(); err != nil {
			t.Fatal(err)
		}
		updated := receiveHTTPResponse(t, updateResponse)
		if updated == nil {
			t.Fatal("region-update request failed")
		}
		defer updated.Body.Close()
		if updated.StatusCode != http.StatusOK {
			t.Fatalf("region-update status = %d; want 200", updated.StatusCode)
		}

		opened := receiveHTTPResponse(t, windowResponse)
		if opened == nil {
			t.Fatal("posting-window request failed")
		}
		defer opened.Body.Close()
		if opened.StatusCode != http.StatusCreated {
			t.Fatalf("posting-window status = %d; want 201", opened.StatusCode)
		}
		var window openPostingWindowResponse
		decodeResponse(t, opened, &window)
		assertUserNotified(t, db, userID, window.ID, true)
	})

	t.Run("window snapshot precedes region update", func(t *testing.T) {
		db, server := newUserRegistrationTestServer(t, true)
		defer server.Close()
		userID := insertTestUser(t, db, "window-first", "europe")

		const advisoryKey int64 = 94721585
		advisoryLock := holdTestAdvisoryLock(t, db, advisoryKey)
		defer advisoryLock.Rollback()
		if _, err := db.Exec(fmt.Sprintf(`
			CREATE FUNCTION hold_window_insert() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN
				PERFORM pg_advisory_xact_lock(%d);
				RETURN NEW;
			END;
			$$
		`, advisoryKey)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TRIGGER hold_window_insert BEFORE INSERT ON posting_windows FOR EACH ROW EXECUTE FUNCTION hold_window_insert()`); err != nil {
			t.Fatal(err)
		}

		windowResponse := make(chan *http.Response, 1)
		go func() {
			response, err := sendJSON(server.Client(), http.MethodPost, server.URL+"/posting-window", openPostingWindowRequest{Region: "asia", LocalDate: "2030-02-02"})
			if err != nil {
				windowResponse <- nil
				return
			}
			windowResponse <- response
		}()
		waitForTableLock(t, db, "ShareRowExclusiveLock", true)

		updateResponse := make(chan *http.Response, 1)
		go func() {
			response, err := sendJSONForUser(server.Client(), server.URL+"/me", userID, updateMeRequest{Region: registrationRegionPointer("asia")})
			if err != nil {
				updateResponse <- nil
				return
			}
			updateResponse <- response
		}()
		waitForTableLock(t, db, "RowExclusiveLock", false)

		if err := advisoryLock.Commit(); err != nil {
			t.Fatal(err)
		}
		opened := receiveHTTPResponse(t, windowResponse)
		if opened == nil {
			t.Fatal("posting-window request failed")
		}
		defer opened.Body.Close()
		if opened.StatusCode != http.StatusCreated {
			t.Fatalf("posting-window status = %d; want 201", opened.StatusCode)
		}
		var window openPostingWindowResponse
		decodeResponse(t, opened, &window)

		updated := receiveHTTPResponse(t, updateResponse)
		if updated == nil {
			t.Fatal("region-update request failed")
		}
		defer updated.Body.Close()
		if updated.StatusCode != http.StatusOK {
			t.Fatalf("region-update status = %d; want 200", updated.StatusCode)
		}
		assertUserNotified(t, db, userID, window.ID, false)
	})
}

func TestUserUsernameMigrationRejectsExistingDuplicates(t *testing.T) {
	db, _ := newUserRegistrationTestDatabase(t, false)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ('first', 'duplicate', $1, 'First', 'asia'), ('second', 'duplicate', $1, 'Second', 'asia')`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}

	err := applyUserUsernameMigration(db)
	if err == nil {
		t.Fatal("migration succeeded with duplicate usernames; want unique-index failure")
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM users WHERE username = 'duplicate'`); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("migration changed duplicate user rows: count = %d", count)
	}
}

func newUserRegistrationTestServer(t *testing.T, migrate bool) (*sqlx.DB, *httptest.Server) {
	t.Helper()
	db, _ := newUserRegistrationTestDatabase(t, migrate)
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.POST("/register", registerHandler(h))
	e.POST("/login", loginHandler(h))
	e.POST("/posting-window", openPostingWindowHandler(h))
	e.PATCH("/me", updateMeHandler(h), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set("session", &Session{UserID: c.Request().Header.Get("X-Test-User-ID")})
			return next(c)
		}
	})
	server := httptest.NewServer(e)
	server.Client().Timeout = 7 * time.Second
	return db, server
}

func newUserRegistrationTestDatabase(t *testing.T, migrate bool) (*sqlx.DB, string) {
	t.Helper()
	dsn := os.Getenv("ISUREAL_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_TEST_DATABASE_DSN is not set")
	}
	ctx := context.Background()
	admin, err := sqlx.ConnectContext(ctx, "pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "user_registration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	config.RuntimeParams["application_name"] = schema
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	connString := stdlib.RegisterConnConfig(config)
	db, err := sqlx.ConnectContext(ctx, "pgx", connString)
	if err != nil {
		admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		t.Fatal(err)
	}
	db.SetMaxOpenConns(32)
	db.SetMaxIdleConns(32)
	t.Cleanup(func() {
		db.Close()
		admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		stdlib.UnregisterConnConfig(connString)
	})
	for _, statement := range []string{
		`CREATE TABLE users (
			id varchar(36) PRIMARY KEY,
			username varchar(64) NOT NULL,
			secret_key_hash text NOT NULL,
			display_name text NOT NULL,
			bio text NOT NULL DEFAULT '',
			profile_image_id varchar(36),
			region text NOT NULL,
			account_type text NOT NULL DEFAULT 'regular',
			status text NOT NULL DEFAULT 'active',
			deleted_at timestamptz,
			created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
			updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
		)`,
		`CREATE TABLE posting_windows (
			id varchar(36) PRIMARY KEY,
			region text NOT NULL,
			local_date date NOT NULL,
			starts_at timestamptz NOT NULL,
			on_time_deadline timestamptz NOT NULL,
			ended_at timestamptz,
			created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
			UNIQUE(region, local_date)
		)`,
		`CREATE UNIQUE INDEX posting_windows_one_active_region ON posting_windows(region) WHERE ended_at IS NULL`,
		`CREATE TABLE notifications (
			id varchar(36) PRIMARY KEY,
			user_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
			posting_window_id varchar(36) NOT NULL REFERENCES posting_windows(id) ON DELETE RESTRICT,
			created_at timestamptz NOT NULL,
			UNIQUE(user_id, posting_window_id)
		)`,
		`CREATE TABLE login_sessions (
			id varchar(36) PRIMARY KEY,
			user_id varchar(36) NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
			token_hash text NOT NULL,
			active boolean NOT NULL DEFAULT true,
			invalidated_at timestamptz,
			reason text,
			created_at timestamptz NOT NULL DEFAULT clock_timestamp()
		)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if migrate {
		if err := applyUserUsernameMigration(db); err != nil {
			t.Fatal(err)
		}
	}
	return db, schema
}

func applyUserUsernameMigration(db *sqlx.DB) error {
	sql, err := os.ReadFile("../sql/004-users-username-unique.sql")
	if err != nil {
		return err
	}
	for _, statement := range strings.Split(string(sql), ";\n") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func waitForTableLock(t *testing.T, db *sqlx.DB, mode string, granted bool) {
	t.Helper()
	query := `
		SELECT EXISTS (
			SELECT 1
			FROM pg_locks l
			JOIN pg_class c ON c.oid = l.relation
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = current_schema()
			  AND c.relname = 'users'
			  AND l.mode = $1
			  AND l.granted = $2
			  AND l.pid <> pg_backend_pid()
		)
	`
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var found bool
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := db.GetContext(ctx, &found, query, mode, granted)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if found {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not observe users %s lock with granted=%t", mode, granted)
}

func postJSON(t *testing.T, client *http.Client, endpoint string, body any) *http.Response {
	t.Helper()
	response, err := sendJSON(client, http.MethodPost, endpoint, body)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func sendJSON(client *http.Client, method, endpoint string, body any) (*http.Response, error) {
	return sendJSONWithHeaders(client, method, endpoint, body, "")
}

func sendJSONForUser(client *http.Client, endpoint, userID string, body any) (*http.Response, error) {
	return sendJSONWithHeaders(client, http.MethodPatch, endpoint, body, userID)
}

func sendJSONWithHeaders(client *http.Client, method, endpoint string, body any, userID string) (*http.Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(method, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if userID != "" {
		request.Header.Set("X-Test-User-ID", userID)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func insertTestUser(t *testing.T, db *sqlx.DB, username, region string) string {
	t.Helper()
	userID := uuid.NewString()
	_, err := db.Exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, $2, $3, $2, $4)`, userID, username, strings.Repeat("0", 64), region)
	if err != nil {
		t.Fatal(err)
	}
	return userID
}

func holdTestAdvisoryLock(t *testing.T, db *sqlx.DB, key int64) *sqlx.Tx {
	t.Helper()
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, key); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	return tx
}

func assertUserNotified(t *testing.T, db *sqlx.DB, userID, windowID string, want bool) {
	t.Helper()
	var got bool
	if err := db.Get(&got, `SELECT EXISTS(SELECT 1 FROM notifications WHERE user_id = $1 AND posting_window_id = $2)`, userID, windowID); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("user notification exists = %t; want %t", got, want)
	}
}

func registrationRegionPointer(value string) *string { return &value }

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func receiveHTTPResponse(t *testing.T, responses <-chan *http.Response) *http.Response {
	t.Helper()
	select {
	case response := <-responses:
		return response
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP request did not complete after releasing its database barrier")
		return nil
	}
}
