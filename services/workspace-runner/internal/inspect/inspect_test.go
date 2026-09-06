package inspect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryProducesMetadataOnly(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "repo", ".github", "workflows"), 0700)
	_ = os.WriteFile(filepath.Join(root, "repo", "go.mod"), []byte("secret-like-content-must-not-return"), 0600)
	_ = os.WriteFile(filepath.Join(root, "repo", ".github", "workflows", "ci.yml"), []byte("x"), 0600)
	_ = os.WriteFile(filepath.Join(root, "repo", ".gitmodules"), []byte("x"), 0600)
	a, err := Directory(root, "owner/repo", "0123456789012345678901234567890123456789")
	if err != nil {
		t.Fatal(err)
	}
	if a.RegularFiles != 3 || !a.CIConfiguration || !a.Gitmodules || a.SourceTrust != "untrusted_repository_data" {
		t.Fatalf("artifact=%+v", a)
	}
}
