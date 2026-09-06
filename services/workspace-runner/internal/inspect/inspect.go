package inspect

import (
	"github.com/devpilot/devpilot/services/workspace-runner/internal/model"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var recognized = map[string]string{"go.mod": "Go", "go.work": "Go", "package.json": "Node.js", "package-lock.json": "Node.js", "pnpm-lock.yaml": "Node.js", "yarn.lock": "Node.js", "pyproject.toml": "Python", "requirements.txt": "Python", "cargo.toml": "Rust", "pom.xml": "Java", "build.gradle": "Java", "build.gradle.kts": "Kotlin"}

func Directory(root, repository, sha string) (model.Artifact, error) {
	start := time.Now()
	a := model.Artifact{Repository: repository, CommitSHA: sha, Extensions: map[string]int{}, SourceTrust: "untrusted_repository_data"}
	ecos := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		slash := filepath.ToSlash(rel)
		depth := len(strings.Split(slash, "/"))
		if depth > a.MaximumDepth {
			a.MaximumDepth = depth
		}
		name := strings.ToLower(d.Name())
		if d.IsDir() {
			a.Directories++
			if strings.Contains("/"+slash+"/", "/.github/workflows/") {
				a.CIConfiguration = true
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			a.Symlinks++
			return nil
		}
		if !info.Mode().IsRegular() {
			a.SuspiciousEntries++
			return nil
		}
		a.RegularFiles++
		a.TotalBytes += info.Size()
		if info.Size() >= 10<<20 {
			a.LargeFiles++
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext == "" {
			ext = "[none]"
		}
		a.Extensions[ext]++
		base := strings.ToLower(filepath.Base(name))
		if language, ok := recognized[base]; ok {
			a.Manifests = append(a.Manifests, slash)
			ecos[language] = true
		}
		if base == ".gitmodules" {
			a.Gitmodules = true
		}
		if base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || base == "docker-compose.yml" || base == "docker-compose.yaml" || base == "compose.yml" || base == "compose.yaml" {
			a.ContainerConfiguration = true
		}
		return nil
	})
	for e := range ecos {
		a.Ecosystems = append(a.Ecosystems, e)
	}
	sort.Strings(a.Ecosystems)
	sort.Strings(a.Manifests)
	a.DurationMilliseconds = time.Since(start).Milliseconds()
	return a, err
}
