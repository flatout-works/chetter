package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	serverURL := os.Getenv("CHETTER_SERVER_URL")
	token := os.Getenv("CHETTER_TOKEN")

	if len(os.Args) < 2 {
		printUsage()
		return nil
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "token":
		if len(args) < 1 {
			printTokenUsage()
			return nil
		}
		return tokenCmd(args, serverURL, token)
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func tokenCmd(args []string, serverURL, token string) error {
	sub := args[0]
	fs := flag.NewFlagSet("token "+sub, flag.ExitOnError)
	server := fs.String("server", serverURL, "Chetter server URL (or set CHETTER_SERVER_URL)")
	tok := fs.String("token", token, "Admin API token (or set CHETTER_TOKEN)")

	switch sub {
	case "create":
		team := fs.String("team", "", "Team name")
		user := fs.String("user", "", "User name")
		tokenName := fs.String("name", "", "Token name (e.g. 'alice-cli')")
		_ = fs.Parse(args[1:])
		if *server == "" {
			return fmt.Errorf("--server or CHETTER_SERVER_URL is required")
		}
		if *tok == "" {
			return fmt.Errorf("--token or CHETTER_TOKEN is required")
		}
		if *team == "" || *user == "" || *tokenName == "" {
			return fmt.Errorf("--team, --user, and --name are required")
		}
		body, _ := json.Marshal(map[string]string{
			"team_name":  *team,
			"user_name":  *user,
			"token_name": *tokenName,
		})
		resp, err := apiPost(*server, *tok, "/api/v1/tokens", body)
		if err != nil {
			return err
		}
		printJSON(resp)
		fmt.Println()
		fmt.Println("Save this token. It will not be shown again.")
		fmt.Println()
		fmt.Println("Tag usage tips:")
		fmt.Println(`  The team name is your scope — all work created with this token`)
		fmt.Println(`  is automatically owned by the team. Use distinct team names like:`)
		fmt.Println(`    "platform", "frontend", "data"`)
		fmt.Println()
		fmt.Println(`  For per-service grouping, create multiple teams:`)
		fmt.Println(`    "platform-api", "platform-worker", "frontend-web"`)
		fmt.Println(`  Or use a team like "platform" and group by git_url in list views.`)
		fmt.Println("  Tasks and schedules already carry the repo (git_url) field.")

	case "list":
		_ = fs.Parse(args[1:])
		if *server == "" {
			return fmt.Errorf("--server or CHETTER_SERVER_URL is required")
		}
		if *tok == "" {
			return fmt.Errorf("--token or CHETTER_TOKEN is required")
		}
		resp, err := apiGet(*server, *tok, "/api/v1/tokens")
		if err != nil {
			return err
		}
		printJSON(resp)

	case "delete":
		name := fs.String("name", "", "Token name to delete")
		_ = fs.Parse(args[1:])
		if *server == "" {
			return fmt.Errorf("--server or CHETTER_SERVER_URL is required")
		}
		if *tok == "" {
			return fmt.Errorf("--token or CHETTER_TOKEN is required")
		}
		if *name == "" {
			return fmt.Errorf("--name is required")
		}
		resp, err := apiDelete(*server, *tok, "/api/v1/tokens/"+*name)
		if err != nil {
			return err
		}
		printJSON(resp)

	default:
		printTokenUsage()
	}
	return nil
}

func apiGet(serverURL, token, path string) ([]byte, error) {
	url := strings.TrimRight(serverURL, "/") + path
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func apiPost(serverURL, token, path string, body []byte) ([]byte, error) {
	url := strings.TrimRight(serverURL, "/") + path
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

func apiDelete(serverURL, token, path string) ([]byte, error) {
	url := strings.TrimRight(serverURL, "/") + path
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func printJSON(data []byte) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		fmt.Println(string(data))
		return
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}

func printUsage() {
	fmt.Println(`chetterctl - Chetter CLI

Usage:
  chetterctl token create --team <team> --user <user> --name <name>
  chetterctl token list
  chetterctl token delete --name <name>

Environment:
  CHETTER_SERVER_URL   Server URL (default: http://localhost:8080)
  CHETTER_TOKEN        Admin API token

Flags can also be set via env vars.`)
}

func printTokenUsage() {
	fmt.Println(`chetterctl token - Manage API tokens

Usage:
  chetterctl token create --team <name> --user <name> --name <token-name>
  chetterctl token list
  chetterctl token delete --name <token-name>

Options:
  --server  Server URL (or CHETTER_SERVER_URL)
  --token   Admin API token (or CHETTER_TOKEN)`)
}
