package isureal

import (
	"os"
	"testing"
)

func TestConfigFromEnvMaxIdleConns(t *testing.T) {
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
