package sandbox

import (
	"strings"
	"testing"
)

func TestDockerSandboxBoundary(t *testing.T) {
	d := Docker{Image: "pinned-inspector@sha256:abc", Memory: "768m", CPUs: "1", PIDs: 64}
	joined := strings.Join(d.arguments("workspace", "/controlled/input", "/controlled/output", "metadata"), " ")
	required := []string{"--network none", "--read-only", "--cap-drop ALL", "no-new-privileges:true", "--pids-limit 64", "--memory 768m", "--cpus 1", "--user 65532:65532", "dst=/workspace,readonly", "/tmp:rw,noexec,nosuid"}
	for _, value := range required {
		if !strings.Contains(joined, value) {
			t.Fatalf("missing %q in %s", value, joined)
		}
	}
	for _, forbidden := range []string{"docker.sock", "containerd.sock", "--privileged", "--network host", "POSTGRES", "GITHUB"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("unsafe sandbox argument %q", forbidden)
		}
	}
}
