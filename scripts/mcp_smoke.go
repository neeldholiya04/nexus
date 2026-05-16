//go:build ignore

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

func main() {
	binary := "./bin/nexus"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		fmt.Println("Build first: go build -o " + binary + " ./cmd/nexus/")
		os.Exit(1)
	}

	cmd := exec.Command(binary, "serve", "--transport", "stdio")
	cmd.Env = append(os.Environ(), "NEXUS_APP_DRY_RUN=true", "NEXUS_LOG_LEVEL=debug")

	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()

	if err := cmd.Start(); err != nil {
		fmt.Printf("failed to start: %v\n", err)
		os.Exit(1)
	}
	defer cmd.Process.Kill()

	time.Sleep(200 * time.Millisecond)

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "smoke-test", "version": "0.0.1"},
		},
	}

	b, _ := json.Marshal(req)
	fmt.Fprintln(stdin, string(b))

	done := make(chan string, 1)
	failed := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) == 0 {
				continue
			}
			if line[0] != '{' {
				failed <- line
				return
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(line), &out); err != nil {
				failed <- fmt.Sprintf("invalid JSON response: %v", err)
				return
			}
			if _, ok := out["result"]; ok {
				done <- line
				return
			}
			failed <- "missing result in response: " + line
			return
		}
	}()

	select {
	case resp := <-done:
		var out map[string]any
		if err := json.Unmarshal([]byte(resp), &out); err != nil {
			fmt.Printf("FAIL: invalid JSON response: %v\n", err)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Printf("OK: MCP server responded:\n%s\n", b)
	case msg := <-failed:
		fmt.Printf("FAIL: unexpected stdout before JSON-RPC response: %s\n", msg)
		os.Exit(1)
	case <-time.After(3 * time.Second):
		fmt.Println("FAIL: no response within 3s")
		os.Exit(1)
	}
}
