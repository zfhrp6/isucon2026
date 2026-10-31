package isureal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
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

func TestCreatePostAllowsConcurrentAuthorsInOneWindow(t *testing.T) {
	db := newPostingWindowTestDB(t)
	seedPostingWindowTestData(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// An unrelated transaction holding a shared period lock models an in-flight
	// post. Other authors must complete while that transaction remains open.
	blocker, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.ExecContext(ctx, `SELECT id FROM posting_windows WHERE id = 'open' FOR SHARE`); err != nil {
		_ = blocker.Rollback()
		t.Fatal(err)
	}

	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	handler := createPostHandler(&HandlerContext{DB: db})
	results := make(chan int, 2)
	requestCtx, stopRequests := context.WithTimeout(ctx, 5*time.Second)
	var workers sync.WaitGroup
	for _, userID := range []string{"author-1", "author-2"} {
		userID := userID
		request := newPostingWindowRequest(t, requestCtx, userID)
		workers.Add(1)
		go func() {
			defer workers.Done()
			recorder := httptest.NewRecorder()
			c := e.NewContext(request, recorder)
			c.Set("session", &Session{UserID: userID})
			if err := handler(c); err != nil {
				e.HTTPErrorHandler(c, err)
			}
			results <- recorder.Code
		}()
	}

	statuses := make([]int, 0, 2)
	defer func() {
		stopRequests()
		_ = blocker.Rollback()
		waitForPostWorkers(t, &workers)
	}()
	deadline := time.NewTimer(2 * time.Second)
collect:
	for len(statuses) < 2 {
		select {
		case status := <-results:
			statuses = append(statuses, status)
		case <-deadline.C:
			break collect
		}
	}
	deadline.Stop()
	stopRequests()
	if err := blocker.Rollback(); err != nil {
		t.Errorf("release period lock: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("posts did not complete while the other transaction held FOR SHARE; got statuses %v", statuses)
	}
	for _, status := range statuses {
		if status != http.StatusCreated {
			t.Errorf("post status = %d, want %d", status, http.StatusCreated)
		}
	}

	var postCount int
	if err := db.GetContext(ctx, &postCount, `SELECT count(*) FROM posts`); err != nil {
		t.Fatal(err)
	}
	if postCount != 2 {
		t.Fatalf("concurrent posts = %d, want 2", postCount)
	}
}

func TestCreatePostSerializesSameAuthorPostSlot(t *testing.T) {
	db := newPostingWindowTestDB(t)
	seedPostingWindowTestData(t, db)
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	handler := createPostHandler(&HandlerContext{DB: db})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	requestCtx, stopRequests := context.WithTimeout(ctx, 5*time.Second)
	defer stopRequests()
	statuses := make(chan int, 2)
	var workers sync.WaitGroup
	defer func() {
		stopRequests()
		waitForPostWorkers(t, &workers)
	}()
	for range 2 {
		request := newPostingWindowRequest(t, requestCtx, "author-1")
		workers.Add(1)
		go func() {
			defer workers.Done()
			recorder := httptest.NewRecorder()
			c := e.NewContext(request, recorder)
			c.Set("session", &Session{UserID: "author-1"})
			if err := handler(c); err != nil {
				e.HTTPErrorHandler(c, err)
			}
			statuses <- recorder.Code
		}()
	}
	first, second := receivePostStatus(t, statuses), receivePostStatus(t, statuses)
	stopRequests()
	waitForPostWorkers(t, &workers)
	if !((first == http.StatusCreated && second == http.StatusConflict) || (second == http.StatusCreated && first == http.StatusConflict)) {
		t.Fatalf("same-author concurrent post statuses = %d and %d, want 201 and 409", first, second)
	}
}

func TestCreatePostKeepsWindowOpenUntilCommit(t *testing.T) {
	db := newPostingWindowTestDB(t)
	seedPostingWindowTestData(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION pause_image_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(722, 14);
  RETURN NEW;
END
$$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER pause_image_insert BEFORE INSERT ON image_objects FOR EACH ROW EXECUTE FUNCTION pause_image_insert()`); err != nil {
		t.Fatal(err)
	}

	// Hold the trigger's advisory lock in a separate transaction so the post
	// pauses after acquiring the posting-window row lock and before it commits.
	imageBlocker, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := imageBlocker.ExecContext(ctx, `SELECT pg_advisory_xact_lock(722, 14)`); err != nil {
		_ = imageBlocker.Rollback()
		t.Fatal(err)
	}

	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	handler := createPostHandler(&HandlerContext{DB: db})
	requestCtx, stopPost := context.WithTimeout(ctx, 7*time.Second)
	request := newPostingWindowRequest(t, requestCtx, "author-1")
	postDone := make(chan int, 1)
	var postWorker sync.WaitGroup
	postWorker.Add(1)
	defer func() {
		stopPost()
		_ = imageBlocker.Rollback()
		waitForPostWorkers(t, &postWorker)
	}()
	go func() {
		defer postWorker.Done()
		recorder := httptest.NewRecorder()
		c := e.NewContext(request, recorder)
		c.Set("session", &Session{UserID: "author-1"})
		if err := handler(c); err != nil {
			e.HTTPErrorHandler(c, err)
		}
		postDone <- recorder.Code
	}()

	appName := postingWindowTestApplicationName(t, db, ctx)
	postPID, err := waitForActivity(t, db, ctx, appName, `query LIKE '%INSERT INTO image_objects%'`)
	if err != nil {
		t.Fatal(err)
	}

	updateCtx, stopUpdate := context.WithTimeout(ctx, 5*time.Second)
	defer stopUpdate()
	windowUpdate := make(chan error, 1)
	var updateWorker sync.WaitGroup
	updateWorker.Add(1)
	defer func() {
		stopUpdate()
		waitForPostWorkers(t, &updateWorker)
	}()
	go func() {
		defer updateWorker.Done()
		_, err := db.ExecContext(updateCtx, `UPDATE posting_windows SET ended_at = clock_timestamp() WHERE id = 'open'`)
		windowUpdate <- err
	}()

	updatePID, err := waitForBlockedActivity(t, db, ctx, appName, postPID)
	if err != nil {
		t.Fatal(err)
	}
	if updatePID == postPID {
		t.Fatal("period update unexpectedly ran on the posting backend")
	}
	if err := imageBlocker.Rollback(); err != nil {
		t.Fatalf("release image insert blocker: %v", err)
	}
	if status := receivePostStatus(t, postDone); status != http.StatusCreated {
		t.Fatalf("post status = %d, want %d", status, http.StatusCreated)
	}
	waitForPostWorkers(t, &postWorker)
	select {
	case err := <-windowUpdate:
		if err != nil {
			t.Fatalf("end posting window after post commit: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("period update remained blocked after post commit: %v", ctx.Err())
	}
	waitForPostWorkers(t, &updateWorker)
}

func TestCreatePostRetriesAfterPostingWindowRollover(t *testing.T) {
	db := newPostingWindowTestDB(t)
	seedPostingWindowTestData(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rollover, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var rolloverPID int
	if err := rollover.GetContext(ctx, &rolloverPID, `SELECT pg_backend_pid()`); err != nil {
		_ = rollover.Rollback()
		t.Fatal(err)
	}
	if _, err := rollover.ExecContext(ctx, `UPDATE posting_windows SET ended_at = clock_timestamp() WHERE id = 'open'`); err != nil {
		_ = rollover.Rollback()
		t.Fatal(err)
	}
	newWindowID := uuid.NewString()
	if _, err := rollover.ExecContext(ctx, `INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ($1, 'asia', CURRENT_DATE + 1, clock_timestamp() - interval '1 minute', clock_timestamp() + interval '1 day')`, newWindowID); err != nil {
		_ = rollover.Rollback()
		t.Fatal(err)
	}

	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	handler := createPostHandler(&HandlerContext{DB: db})
	requestCtx, stopPost := context.WithTimeout(ctx, 7*time.Second)
	request := newPostingWindowRequest(t, requestCtx, "author-1")
	postDone := make(chan postResult, 1)
	var postWorker sync.WaitGroup
	postWorker.Add(1)
	defer func() {
		stopPost()
		_ = rollover.Rollback()
		waitForPostWorkers(t, &postWorker)
	}()
	go func() {
		defer postWorker.Done()
		recorder := httptest.NewRecorder()
		c := e.NewContext(request, recorder)
		c.Set("session", &Session{UserID: "author-1"})
		if err := handler(c); err != nil {
			e.HTTPErrorHandler(c, err)
		}
		postDone <- postResult{status: recorder.Code, body: append([]byte(nil), recorder.Body.Bytes()...)}
	}()

	appName := postingWindowTestApplicationName(t, db, ctx)
	if _, err := waitForBlockedByPID(t, db, ctx, appName, rolloverPID, `query LIKE '%FROM posting_windows WHERE region =%'`); err != nil {
		t.Fatal(err)
	}
	if err := rollover.Commit(); err != nil {
		t.Fatalf("commit period rollover: %v", err)
	}
	result := receivePostResult(t, postDone)
	stopPost()
	waitForPostWorkers(t, &postWorker)
	if result.status != http.StatusCreated {
		t.Fatalf("post status = %d, want %d: %s", result.status, http.StatusCreated, result.body)
	}
	var response createPostResponse
	if err := json.Unmarshal(result.body, &response); err != nil {
		t.Fatalf("decode post response: %v", err)
	}
	if response.PostingWindowID != newWindowID {
		t.Fatalf("post used window %q, want rolled-over window %q", response.PostingWindowID, newWindowID)
	}
}

func TestCreatePostEnforcesDeletedPostLimit(t *testing.T) {
	db := newPostingWindowTestDB(t)
	seedPostingWindowTestData(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	postHandler := createPostHandler(&HandlerContext{DB: db})
	e.POST("/api/posts", func(c *echo.Context) error {
		c.Set("session", &Session{UserID: "author-1"})
		return postHandler(c)
	})
	deleteHandler := deletePostHandler(&HandlerContext{DB: db})
	e.DELETE("/api/posts/:postId", func(c *echo.Context) error {
		c.Set("session", &Session{UserID: "author-1"})
		return deleteHandler(c)
	})

	for deletion := range 2 {
		postRecorder := httptest.NewRecorder()
		postReq := newPostingWindowRequest(t, ctx, "author-1")
		e.ServeHTTP(postRecorder, postReq)
		if postRecorder.Code != http.StatusCreated {
			t.Fatalf("post %d status = %d, want %d: %s", deletion+1, postRecorder.Code, http.StatusCreated, postRecorder.Body.String())
		}
		var post createPostResponse
		if err := json.Unmarshal(postRecorder.Body.Bytes(), &post); err != nil {
			t.Fatalf("decode post response: %v", err)
		}
		deleteReq := httptest.NewRequest(http.MethodDelete, "/api/posts/"+post.ID, nil).WithContext(ctx)
		deleteRecorder := httptest.NewRecorder()
		e.ServeHTTP(deleteRecorder, deleteReq)
		if deleteRecorder.Code != http.StatusNoContent {
			t.Fatalf("delete %d status = %d, want %d: %s", deletion+1, deleteRecorder.Code, http.StatusNoContent, deleteRecorder.Body.String())
		}
	}

	postRecorder := httptest.NewRecorder()
	e.ServeHTTP(postRecorder, newPostingWindowRequest(t, ctx, "author-1"))
	if postRecorder.Code != http.StatusConflict {
		t.Fatalf("post after two deletions status = %d, want %d: %s", postRecorder.Code, http.StatusConflict, postRecorder.Body.String())
	}
	var deletedCount, activeCount int
	if err := db.QueryRowxContext(ctx, `SELECT count(*) FILTER (WHERE deleted_at IS NOT NULL), count(*) FILTER (WHERE deleted_at IS NULL) FROM posts`).Scan(&deletedCount, &activeCount); err != nil {
		t.Fatal(err)
	}
	if deletedCount != 2 || activeCount != 0 {
		t.Fatalf("post state after rejected attempt: deleted=%d active=%d, want deleted=2 active=0", deletedCount, activeCount)
	}
}

type postResult struct {
	status int
	body   []byte
}

func newPostingWindowTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("ISUREAL_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_TEST_DATABASE_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	adminDB, err := sqlx.ConnectContext(ctx, "pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "posting_window_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		adminDB.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := adminDB.ExecContext(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		adminDB.Close()
	})

	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	config.RuntimeParams["application_name"] = "posting-window-test-" + schema
	db := sqlx.NewDb(stdlib.OpenDB(*config), "pgx")
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`CREATE TABLE users (id text PRIMARY KEY, username text NOT NULL UNIQUE, secret_key_hash text NOT NULL, display_name text NOT NULL, bio text NOT NULL, profile_image_id text, region text NOT NULL, account_type text NOT NULL, status text NOT NULL)`,
		`CREATE TABLE posting_windows (id text PRIMARY KEY, region text NOT NULL, local_date date NOT NULL, starts_at timestamptz NOT NULL, on_time_deadline timestamptz NOT NULL, ended_at timestamptz, UNIQUE (region, local_date))`,
		`CREATE UNIQUE INDEX posting_windows_one_active_region ON posting_windows(region) WHERE ended_at IS NULL`,
		`CREATE TABLE image_objects (id text NOT NULL, owner_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT, data bytea NOT NULL, media_type text NOT NULL, created_at timestamptz NOT NULL, PRIMARY KEY (id, created_at))`,
		`CREATE TABLE posts (id text PRIMARY KEY, author_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT, posting_window_id text NOT NULL REFERENCES posting_windows(id) ON DELETE RESTRICT, image_id text NOT NULL, caption text NOT NULL DEFAULT '', visibility text NOT NULL, location_kind text NOT NULL, latitude numeric, longitude numeric, locality text, posted_at timestamptz NOT NULL, deleted_at timestamptz)`,
	} {
		ddlCtx, ddlCancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := db.ExecContext(ddlCtx, statement)
		ddlCancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedPostingWindowTestData(t *testing.T, db *sqlx.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, userID := range []string{"author-1", "author-2"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users VALUES ($1, $1, $2, $1, '', NULL, 'asia', 'regular', 'active')`, userID, strings.Repeat("0", 64)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO posting_windows VALUES ('open', 'asia', CURRENT_DATE, clock_timestamp() - interval '1 minute', clock_timestamp() + interval '1 day', NULL)`); err != nil {
		t.Fatal(err)
	}
}

func newPostingWindowRequest(t *testing.T, ctx context.Context, userID string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "post.jpg")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.White)
	if err := jpeg.Encode(part, img, nil); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"caption": userID, "visibility": "public", "location_kind": "none"} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/posts", &body).WithContext(ctx)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func postingWindowTestApplicationName(t *testing.T, db *sqlx.DB, ctx context.Context) string {
	t.Helper()
	var appName string
	if err := db.GetContext(ctx, &appName, `SELECT current_setting('application_name')`); err != nil {
		t.Fatal(err)
	}
	return appName
}

func waitForActivity(t *testing.T, db *sqlx.DB, ctx context.Context, appName, predicate string) (int, error) {
	t.Helper()
	query := `SELECT pid FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND application_name = $1 AND state = 'active' AND ` + predicate + ` ORDER BY query_start LIMIT 1`
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var pid int
		if err := db.GetContext(ctx, &pid, query, appName); err == nil {
			return pid, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("query active worker backend: %w", err)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return 0, context.DeadlineExceeded
}

func waitForBlockedActivity(t *testing.T, db *sqlx.DB, ctx context.Context, appName string, blockedPID int) (int, error) {
	return waitForBlockedByPID(t, db, ctx, appName, blockedPID, `query LIKE 'UPDATE posting_windows SET ended_at%'`)
}

func waitForBlockedByPID(t *testing.T, db *sqlx.DB, ctx context.Context, appName string, blockerPID int, predicate string) (int, error) {
	t.Helper()
	query := `SELECT waiter.pid FROM pg_stat_activity waiter WHERE waiter.pid <> pg_backend_pid() AND waiter.application_name = $1 AND waiter.state = 'active' AND waiter.wait_event_type = 'Lock' AND ` + predicate + ` AND $2 = ANY(pg_blocking_pids(waiter.pid)) ORDER BY waiter.query_start LIMIT 1`
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var pid int
		if err := db.GetContext(ctx, &pid, query, appName, blockerPID); err == nil {
			return pid, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("query blocked worker backend: %w", err)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return 0, context.DeadlineExceeded
}

func receivePostStatus(t *testing.T, results <-chan int) int {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for post handler")
		return 0
	}
}

func receivePostResult(t *testing.T, results <-chan postResult) postResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for post handler")
		return postResult{}
	}
}

func waitForPostWorkers(t *testing.T, workers *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for database workers to stop")
	}
}
