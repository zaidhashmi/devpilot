package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/devpilot/devpilot/services/workspace-runner/internal/model"
)

type Provider interface {
	Inspect(context.Context, string, string, string) (model.Artifact, error)
	Terminate(context.Context, string) error
	Cleanup(context.Context, string) error
}

type Docker struct {
	Image  string
	Memory string
	CPUs   string
	PIDs   int
}

func (d Docker) Inspect(ctx context.Context, id, root, metadata string) (model.Artifact, error) {
	out, err := os.MkdirTemp("", "devpilot-result-")
	if err != nil {
		return model.Artifact{}, err
	}
	defer os.RemoveAll(out)
	if err := os.Chmod(out, 0777); err != nil {
		return model.Artifact{}, err
	}
	name := "devpilot-inspect-" + id
	args := d.arguments(name, root, out, metadata)
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return model.Artifact{}, fmt.Errorf("sandbox inspection failed: %s", string(output))
	}
	data, err := os.ReadFile(filepath.Join(out, "result.json"))
	if err != nil {
		return model.Artifact{}, err
	}
	var artifact model.Artifact
	if err = json.Unmarshal(data, &artifact); err != nil {
		return model.Artifact{}, err
	}
	return artifact, nil
}
func (d Docker) arguments(name, root, out, metadata string) []string {
	return []string{"run", "--rm", "--name", name, "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--pids-limit", strconv.Itoa(d.PIDs), "--memory", d.Memory, "--cpus", d.CPUs, "--user", "65532:65532", "--mount", "type=bind,src=" + root + ",dst=/workspace,readonly", "--mount", "type=bind,src=" + out + ",dst=/result", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", d.Image, "inspect-dir", "/workspace", "/result/result.json", metadata}
}
func (d Docker) Terminate(ctx context.Context, id string) error {
	return exec.CommandContext(ctx, "docker", "rm", "-f", "devpilot-inspect-"+id).Run()
}
func (d Docker) Cleanup(context.Context, string) error { return nil }
