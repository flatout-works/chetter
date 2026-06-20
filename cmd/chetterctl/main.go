package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
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
	serverURL := env("CHETTER_SERVER_URL", defaultServerURL)
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
		fmt.Println("  Tasks and schedules already carry the repo (git_url) field.")

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

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func printUsage() {
	fmt.Println(`chetterctl - Chetter CLI

Usage:
  chetterctl token create --team <team> --user <user> --name <name>
  chetterctl token list
  chetterctl token delete --name <name>

Environment:
  CHETTER_SERVER_URL   Server URL (default: http://localhost:8090)
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
