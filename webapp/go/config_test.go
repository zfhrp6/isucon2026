package isureal

import (
	"os"
	"strconv"
	"testing"
)

func TestConfigFromEnvMaxIdleConns(t *testing.T) {
	unsetEnvForTest(t, "ISUREAL_DB_MAX_OPEN_CONNS")
	key := "ISUREAL_DB_MAX_IDLE_CONNS"
	previous, wasSet := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(key, previous)
		} else {
			_ = os.Unsetenv(key)
		}
	})

	if got := ConfigFromEnv().MaxIdleConns; got != 2 {
		t.Fatalf("unset %s: MaxIdleConns = %d, want 2", key, got)
	}

	if err := os.Setenv(key, "0"); err != nil {
		t.Fatal(err)
	}
	if got := ConfigFromEnv().MaxIdleConns; got != 0 {
		t.Fatalf("%s=0: MaxIdleConns = %d, want 0", key, got)
	}
}

func TestConfigFromEnvRejectsInvalidMaxIdleConns(t *testing.T) {
	t.Setenv("ISUREAL_DB_MAX_OPEN_CONNS", "30")
	for _, value := range []string{"", "-1", "31", "two", "2.0"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ISUREAL_DB_MAX_IDLE_CONNS", value)
			defer func() {
				if recover() == nil {
					t.Fatalf("ConfigFromEnv accepted %s=%q", "ISUREAL_DB_MAX_IDLE_CONNS", value)
				}
			}()
			ConfigFromEnv()
		})
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	previous, wasSet := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(key, previous)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestConfigFromEnvMaxOpenConns(t *testing.T) {
	unsetEnvForTest(t, "ISUREAL_DB_MAX_OPEN_CONNS")
	t.Setenv("ISUREAL_DB_MAX_IDLE_CONNS", "2")
	if got := ConfigFromEnv().MaxOpenConns; got != 30 {
		t.Fatalf("unset max open: got %d, want 30", got)
	}
	for _, value := range []string{"40", "25"} {
		t.Setenv("ISUREAL_DB_MAX_OPEN_CONNS", value)
		if got := ConfigFromEnv().MaxOpenConns; got != mustAtoi(t, value) {
			t.Fatalf("max open %s: got %d", value, got)
		}
	}
}

func TestConfigFromEnvRejectsInvalidMaxOpenConnsAndIdleAboveOpen(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "91", "two", "2.0"} {
		t.Run("open_"+value, func(t *testing.T) {
			t.Setenv("ISUREAL_DB_MAX_OPEN_CONNS", value)
			t.Setenv("ISUREAL_DB_MAX_IDLE_CONNS", "2")
			defer func() {
				if recover() == nil {
					t.Fatalf("ConfigFromEnv accepted max open %q", value)
				}
			}()
			ConfigFromEnv()
		})
	}
	t.Setenv("ISUREAL_DB_MAX_OPEN_CONNS", "15")
	t.Setenv("ISUREAL_DB_MAX_IDLE_CONNS", "16")
	defer func() {
		if recover() == nil {
			t.Fatal("ConfigFromEnv accepted max idle above max open")
		}
	}()
	ConfigFromEnv()
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	parsed, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
