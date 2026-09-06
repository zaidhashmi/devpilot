package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

type entry struct {
	name string
	kind byte
	body string
	link string
}

func makeArchive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.kind, Size: int64(len(e.body)), Mode: 0600, Linkname: e.link}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.kind == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}
func limits() Limits {
	return Limits{MaxExpandedBytes: 1024, MaxFileBytes: 512, MaxFiles: 4, MaxDepth: 5}
}
func TestExtractRegularAndNestedArchiveAsData(t *testing.T) {
	root := t.TempDir()
	payload := makeArchive(t, entry{"repo/a.txt", tar.TypeReg, "ok", ""}, entry{"repo/nested.zip", tar.TypeReg, "not really an archive", ""})
	stats, err := ExtractTarGzip(bytes.NewReader(payload), root, limits())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 2 {
		t.Fatalf("files=%d", stats.Files)
	}
	if _, err := os.Stat(filepath.Join(root, "repo", "nested.zip")); err != nil {
		t.Fatal(err)
	}
}
func TestRejectHostileEntries(t *testing.T) {
	cases := map[string][]entry{
		"parent traversal": {{"../escape", tar.TypeReg, "x", ""}}, "windows traversal": {{"..\\escape", tar.TypeReg, "x", ""}}, "windows absolute": {{"C:\\escape", tar.TypeReg, "x", ""}}, "absolute": {{"/escape", tar.TypeReg, "x", ""}}, "symlink inventory only": {{"repo/link", tar.TypeSymlink, "", "../../escape"}}, "hardlink": {{"repo/link", tar.TypeLink, "", "repo/a"}}, "device": {{"repo/device", tar.TypeChar, "", ""}}, "too deep": {{"a/b/c/d/e/f", tar.TypeReg, "x", ""}}, "file too large": {{"repo/a", tar.TypeReg, string(make([]byte, 513)), ""}}, "malformed": nil,
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			var data []byte
			if entries == nil {
				data = []byte("broken")
			} else {
				data = makeArchive(t, entries...)
			}
			stats, err := ExtractTarGzip(bytes.NewReader(data), root, limits())
			if name == "symlink inventory only" {
				if err != nil || stats.Symlinks != 1 {
					t.Fatalf("stats=%+v err=%v", stats, err)
				}
				if _, err := os.Lstat(filepath.Join(root, "repo", "link")); !os.IsNotExist(err) {
					t.Fatal("symlink was materialized")
				}
				return
			}
			if err == nil {
				t.Fatal("expected rejection")
			}
			if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
				t.Fatal("entry escaped")
			}
		})
	}
}
func TestUnusualUnicodeFilenameIsContained(t *testing.T) {
	root := t.TempDir()
	_, err := ExtractTarGzip(bytes.NewReader(makeArchive(t, entry{"repo/こんにちは file.txt", tar.TypeReg, "data", ""})), root, limits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "repo", "こんにちは file.txt")); err != nil {
		t.Fatal(err)
	}
}
func TestLimits(t *testing.T) {
	t.Run("file count", func(t *testing.T) {
		entries := []entry{}
		for i := 0; i < 5; i++ {
			entries = append(entries, entry{name: "repo/" + string(rune('a'+i)), kind: tar.TypeReg, body: "x"})
		}
		_, err := ExtractTarGzip(bytes.NewReader(makeArchive(t, entries...)), t.TempDir(), limits())
		if err == nil {
			t.Fatal("expected limit")
		}
	})
	t.Run("expanded bytes", func(t *testing.T) {
		l := limits()
		l.MaxExpandedBytes = 3
		_, err := ExtractTarGzip(bytes.NewReader(makeArchive(t, entry{"repo/a", tar.TypeReg, "1234", ""})), t.TempDir(), l)
		if err == nil {
			t.Fatal("expected limit")
		}
	})
}
