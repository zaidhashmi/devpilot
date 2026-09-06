package main

import (
	"encoding/json"
	"github.com/devpilot/devpilot/services/workspace-runner/internal/inspect"
	"github.com/devpilot/devpilot/services/workspace-runner/internal/sandbox"
	"github.com/devpilot/devpilot/services/workspace-runner/internal/server"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "inspect-dir" {
		if len(os.Args) != 5 {
			log.Fatal("invalid inspector arguments")
		}
		parts := strings.SplitN(os.Args[4], "\n", 2)
		if len(parts) != 2 {
			log.Fatal("invalid metadata")
		}
		artifact, err := inspect.Directory(os.Args[2], parts[0], parts[1])
		if err != nil {
			log.Fatal(err)
		}
		data, _ := json.Marshal(artifact)
		if err = os.WriteFile(os.Args[3], data, 0600); err != nil {
			log.Fatal(err)
		}
		return
	}
	secret := os.Getenv("DEVPILOT_RUNNER_SHARED_SECRET")
	if len(secret) < 32 {
		log.Fatal("DEVPILOT_RUNNER_SHARED_SECRET must contain at least 32 characters")
	}
	addr := os.Getenv("DEVPILOT_RUNNER_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	provider := sandbox.Docker{Image: env("DEVPILOT_INSPECTOR_IMAGE", "devpilot-workspace-inspector:local"), Memory: env("DEVPILOT_SANDBOX_MEMORY", "768m"), CPUs: env("DEVPILOT_SANDBOX_CPUS", "1"), PIDs: 64}
	log.Fatal(http.ListenAndServe(addr, server.New(secret, provider)))
}
func env(k, v string) string {
	if x := os.Getenv(k); x != "" {
		return x
	}
	return v
}
