package archive

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrUnsafe = errors.New("unsafe archive")

type Limits struct {
	MaxExpandedBytes int64
	MaxFileBytes     int64
	MaxFiles         int
	MaxDepth         int
}
type Stats struct {
	Files       int
	Bytes       int64
	Directories int
	Symlinks    int
	Suspicious  int
	MaxDepth    int
}

func ExtractTarGzip(source io.Reader, root string, limits Limits) (Stats, error) {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return Stats{}, fmt.Errorf("%w: malformed gzip", ErrUnsafe)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var stats Stats
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return stats, fmt.Errorf("%w: malformed tar", ErrUnsafe)
		}
		name := strings.ReplaceAll(h.Name, "\\", "/")
		clean := filepath.ToSlash(filepath.Clean(name))
		parts := strings.Split(strings.Trim(clean, "/"), "/")
		if unsafeName(name, parts) || strings.HasPrefix(name, "/") || filepath.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") || len(name) > 4096 || len(parts) > limits.MaxDepth {
			return stats, fmt.Errorf("%w: invalid path", ErrUnsafe)
		}
		// GitHub archives have one generated top-level directory; preserve it while still containing every write.
		target := filepath.Join(root, filepath.FromSlash(clean))
		rel, err := filepath.Rel(root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return stats, fmt.Errorf("%w: path escape", ErrUnsafe)
		}
		if seen[clean] {
			return stats, fmt.Errorf("%w: duplicate path", ErrUnsafe)
		}
		seen[clean] = true
		depth := len(parts)
		if depth > stats.MaxDepth {
			stats.MaxDepth = depth
		}
		switch h.Typeflag {
		case tar.TypeDir:
			stats.Directories++
			if err := os.MkdirAll(target, 0700); err != nil {
				return stats, err
			}
		case tar.TypeReg, tar.TypeRegA:
			stats.Files++
			if stats.Files > limits.MaxFiles || h.Size < 0 || h.Size > limits.MaxFileBytes || stats.Bytes+h.Size > limits.MaxExpandedBytes {
				return stats, fmt.Errorf("%w: extraction limit", ErrUnsafe)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return stats, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return stats, err
			}
			n, copyErr := io.CopyN(f, tr, h.Size)
			closeErr := f.Close()
			if copyErr != nil || n != h.Size {
				return stats, fmt.Errorf("%w: truncated entry", ErrUnsafe)
			}
			if closeErr != nil {
				return stats, closeErr
			}
			stats.Bytes += n
		case tar.TypeSymlink:
			stats.Symlinks++
			stats.Suspicious++ // inventory only; never materialize or dereference
		case tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo, tar.TypeGNUSparse:
			return stats, fmt.Errorf("%w: unsupported entry", ErrUnsafe)
		default:
			stats.Suspicious++
		}
	}
	return stats, nil
}
func unsafeName(name string, parts []string) bool {
	if name == "" || strings.Contains(name, "\x00") {
		return true
	}
	if len(name) >= 2 && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) && name[1] == ':' {
		return true
	}
	for _, part := range parts {
		if len(part) > 255 {
			return true
		}
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return true
		}
	}
	return false
}
