package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"connectrpc.com/connect"
	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
	"github.com/flatout-works/chetter/gen/proto/api/v1/apiv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const defaultServerURL = "http://localhost:8090"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	serverURL := envAny(defaultServerURL, "CHETTER_API_URL")
	webURL := envAny(serverURL, "CHETTER_WEB_URL")
	token := envAny("", "CHETTER_TOKEN", "MCP_AUTH_TOKEN", "CHETTER_MCP_AUTH_TOKEN")

	if len(os.Args) < 2 {
		printUsage()
		return nil
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "web":
		return webCmd(args, webURL, token)
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
	server := fs.String("server", serverURL, "Chetter web API URL (or set CHETTER_API_URL)")
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
		client := newAdminClient(*server, *tok)
		resp, err := client.CreateToken(context.Background(), connect.NewRequest(&apiv1.CreateTokenRequest{
			TeamName:  *team,
			UserName:  *user,
			TokenName: *tokenName,
		}))
		if err != nil {
			return err
		}
		printProtoJSON(resp.Msg)
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
		fmt.Println("  Tasks and triggers already carry the repo (git_url) field.")

	case "list":
		_ = fs.Parse(args[1:])
		if *server == "" {
			return fmt.Errorf("--server or CHETTER_SERVER_URL is required")
		}
		if *tok == "" {
			return fmt.Errorf("--token or CHETTER_TOKEN is required")
		}
		client := newAdminClient(*server, *tok)
		resp, err := client.ListTokens(context.Background(), connect.NewRequest(&apiv1.ListTokensRequest{}))
		if err != nil {
			return err
		}
		printProtoJSON(resp.Msg)

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
		client := newAdminClient(*server, *tok)
		resp, err := client.DeleteToken(context.Background(), connect.NewRequest(&apiv1.DeleteTokenRequest{Name: *name}))
		if err != nil {
			return err
		}
		printProtoJSON(resp.Msg)

	default:
		printTokenUsage()
	}
	return nil
}

func webCmd(args []string, serverURL, token string) error {
	fs := flag.NewFlagSet("web", flag.ExitOnError)
	server := fs.String("server", serverURL, "Chetter web UI URL (or set CHETTER_WEB_URL)")
	tok := fs.String("token", token, "Admin API token for a login link (or set CHETTER_TOKEN)")
	_ = fs.Parse(args)
	if *server == "" {
		return fmt.Errorf("--server or CHETTER_WEB_URL is required")
	}

	link := strings.TrimRight(*server, "/")
	if *tok != "" {
		link += "#token=" + url.QueryEscape(*tok)
	}

	fmt.Println("Open Chetter web UI:")
	fmt.Println("  " + link)
	if *tok != "" {
		fmt.Println()
		fmt.Println("The token is placed in the URL fragment and stored by the browser UI; it is not sent in the HTTP request for the page.")
	} else {
		fmt.Println()
		fmt.Println("Pass --token or set CHETTER_TOKEN to print a one-click login link.")
	}
	return nil
}

func newAdminClient(serverURL, token string) apiv1connect.AdminServiceClient {
	return apiv1connect.NewAdminServiceClient(
		&authHTTPClient{token: token, next: http.DefaultClient},
		strings.TrimRight(serverURL, "/"),
	)
}

type authHTTPClient struct {
	token string
	next  *http.Client
}

func (c *authHTTPClient) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	return c.next.Do(req)
}

func printProtoJSON(msg proto.Message) {
	out, err := protojson.MarshalOptions{
		Multiline:     true,
		Indent:        "  ",
		UseProtoNames: true,
	}.Marshal(msg)
	if err != nil {
		fmt.Println(msg)
		return
	}
	fmt.Println(string(out))
}

func envAny(fallback string, keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return fallback
}

func printUsage() {
	fmt.Println(`chetterctl - Chetter CLI

Usage:
  chetterctl web
  chetterctl token create --team <team> --user <user> --name <name>
  chetterctl token list
  chetterctl token delete --name <name>

Environment:
  CHETTER_WEB_URL      Web UI URL for chetterctl web (default: http://localhost:8090)
  CHETTER_API_URL      Web API URL for token commands (default: http://localhost:8090)
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
  --server  Web API URL (or CHETTER_API_URL)
  --token   Admin API token (or CHETTER_TOKEN)`)
}
