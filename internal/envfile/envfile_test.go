package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_SetsUnsetVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# a comment\n\nGOOGLE_CLOUD_PROJECT=my-project\nGOOGLE_CLOUD_LOCATION=\"us-central1\"\nGOOGLE_API_KEY='abc123'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	os.Unsetenv("GOOGLE_CLOUD_PROJECT")
	os.Unsetenv("GOOGLE_CLOUD_LOCATION")
	os.Unsetenv("GOOGLE_API_KEY")

	if err := Load(path); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if got := os.Getenv("GOOGLE_CLOUD_PROJECT"); got != "my-project" {
		t.Errorf("GOOGLE_CLOUD_PROJECT = %q, want my-project", got)
	}
	if got := os.Getenv("GOOGLE_CLOUD_LOCATION"); got != "us-central1" {
		t.Errorf("GOOGLE_CLOUD_LOCATION = %q, want us-central1 (quotes stripped)", got)
	}
	if got := os.Getenv("GOOGLE_API_KEY"); got != "abc123" {
		t.Errorf("GOOGLE_API_KEY = %q, want abc123 (quotes stripped)", got)
	}
}

func TestLoad_DoesNotOverrideExistingEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("GOOGLE_API_KEY=from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_API_KEY", "from-real-env")

	if err := Load(path); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if got := os.Getenv("GOOGLE_API_KEY"); got != "from-real-env" {
		t.Errorf("GOOGLE_API_KEY = %q, want from-real-env (real env must win)", got)
	}
}

func TestLoad_MissingFileIsNotAnError(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "does-not-exist.env")); err != nil {
		t.Errorf("Load with a missing file returned an error: %v", err)
	}
}
