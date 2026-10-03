package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Only installation assets are linked; never copy a template's .env/store.
func prepareRuntime(source, root, workspace string) error {
	for _, dir := range []string{root, filepath.Join(root, "var"), filepath.Join(root, "var", "bin")} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		if e := requireDirectory(dir); e != nil {
			return e
		}
	}
	for _, name := range []string{"manifest.yaml", "sdk", "skills", "components", "config.nims", "niffler.nimble"} {
		src := filepath.Join(source, name)
		if _, e := os.Stat(src); e != nil {
			if name == "manifest.yaml" {
				return e
			}
			continue
		}
		if e := trustedLink(src, filepath.Join(root, name)); e != nil {
			return e
		}
	}
	entries, e := os.ReadDir(filepath.Join(source, "var", "bin"))
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if e = trustedLink(filepath.Join(source, "var", "bin", entry.Name()), filepath.Join(root, "var", "bin", entry.Name())); e != nil {
			return e
		}
	}
	agent, e := os.ReadFile(filepath.Join(workspace, ".niffler", "agent.md"))
	if e == nil {
		if e = privateWrite(filepath.Join(root, "AGENTS.override.md"), agent); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	dir := filepath.Join(root, ".agents")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e = requireDirectory(dir); e != nil {
		return e
	}
	if e = trustedLink(filepath.Join(workspace, ".niffler", "skills"), filepath.Join(dir, "skills")); e != nil {
		return e
	}
	return exposeNimPackages(source, workspace)
}
func exposeNimPackages(source, workspace string) error {
	pkgs := filepath.Join(source, "nimble-pkgs2")
	if _, e := os.Stat(pkgs); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	}
	dir := filepath.Join(workspace, ".nimble")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e := requireDirectory(dir); e != nil {
		return e
	}
	return trustedLink(pkgs, filepath.Join(dir, "pkgs2"))
}
func requireDirectory(path string) error {
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("manager directory must not be a symlink: %s", path)
	}
	return nil
}
func privateWrite(path string, body []byte) error {
	if st, e := os.Lstat(path); e == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("manager file must be regular: %s", path)
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e := os.WriteFile(path, body, 0600); e != nil {
		return e
	}
	return os.Chmod(path, 0600)
}
func trustedLink(src, dst string) error {
	if _, e := os.Lstat(dst); os.IsNotExist(e) {
		return os.Symlink(src, dst)
	} else if e != nil {
		return e
	}
	link, e := os.Readlink(dst)
	if e != nil || filepath.Clean(link) != filepath.Clean(src) {
		return fmt.Errorf("untrusted installation asset: %s", dst)
	}
	return nil
}
func (b *bridge) redact(text string) string {
	for _, secret := range b.secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	return text
}
func (b *bridge) loadSecrets() error {
	b.secrets = append(b.secrets, b.password)
	var providers map[string]struct {
		Key string `json:"apiKey"`
	}
	if e := json.Unmarshal([]byte(os.Getenv("NIF_LLM_PROVIDERS")), &providers); e != nil {
		return e
	}
	for _, p := range providers {
		b.secrets = append(b.secrets, p.Key)
	}
	raw, e := os.ReadFile(filepath.Join(b.workspace, ".niffler", "mcp.json"))
	if e != nil {
		return e
	}
	var cfg struct {
		Servers []struct {
			Headers map[string]string `json:"headers"`
		} `json:"servers"`
	}
	if e = json.Unmarshal(raw, &cfg); e != nil {
		return e
	}
	for _, s := range cfg.Servers {
		for _, v := range s.Headers {
			if strings.HasPrefix(v, "Bearer ") {
				token := strings.TrimPrefix(v, "Bearer ")
				if strings.HasPrefix(token, "${") && strings.HasSuffix(token, "}") {
					token = os.Getenv(strings.TrimSuffix(strings.TrimPrefix(token, "${"), "}"))
				}
				b.secrets = append(b.secrets, token)
			}
		}
	}
	return nil
}
