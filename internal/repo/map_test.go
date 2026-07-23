package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuzu-ux/ycode/internal/token"
)

func TestBuildHonorsBudgetAndRanksQuery(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/auth/login.go", "package auth\nfunc LoginUser() {}\n")
	writeTestFile(t, root, "internal/other/cache.go", "package other\nfunc CacheValue() {}\n")
	for index := 0; index < 40; index++ {
		writeTestFile(t, root, filepath.Join("many", string(rune('a'+index%20)), "file"+string(rune('A'+index))+".go"), "package many\nfunc Something() {}\n")
	}

	snapshot, err := Build(root, "fix login auth", 180)
	if err != nil {
		t.Fatal(err)
	}
	if token.EstimateText(snapshot.Text) > 190 {
		t.Fatalf("map exceeded budget: %d tokens\n%s", token.EstimateText(snapshot.Text), snapshot.Text)
	}
	authIndex := strings.Index(snapshot.Text, "internal/auth/login.go")
	otherIndex := strings.Index(snapshot.Text, "internal/other/cache.go")
	if authIndex < 0 || (otherIndex >= 0 && authIndex > otherIndex) {
		t.Fatalf("query-relevant file was not ranked first:\n%s", snapshot.Text)
	}
}

func TestBuildSkipsSecretLikeFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".env", "API_KEY=secret")
	writeTestFile(t, root, "credentials.json", `{"secret":"value"}`)
	writeTestFile(t, root, "main.go", "package main\nfunc main() {}\n")

	snapshot, err := Build(root, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot.Text, ".env") || strings.Contains(snapshot.Text, "credentials") {
		t.Fatalf("secret-like path leaked into map:\n%s", snapshot.Text)
	}
}

func TestMeasureReportsReduction(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "large.go", "package large\n"+strings.Repeat("// context\n", 2000))
	measurement, err := Measure(root, "large", 150)
	if err != nil {
		t.Fatal(err)
	}
	if measurement.NaiveContextTokens <= measurement.MapTokens || measurement.AvoidedTokens <= 0 {
		t.Fatalf("unexpected measurement: %+v", measurement)
	}
}

func writeTestFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
