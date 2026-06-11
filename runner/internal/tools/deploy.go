package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DeployProvider selects the deployment backend: "local" Docker or
// Preview deployment.
type DeployProvider string

const (
	// DeployProviderLocal runs containers directly on the runner host.
	DeployProviderLocal DeployProvider = "local"
	// DeployProviderPreview uses preview deployment (remote Wowbagger).
	DeployProviderPreview DeployProvider = "preview"
)

// Deploy holds MCP tool handlers for building, pushing, running,
// inspecting, and rolling back Docker images within a workspace.
type Deploy struct {
	BaseDir    string
	Provider   DeployProvider
	TaskID     string
	Registry   string
	DevfleetURL string
}

// NewDeploy creates a deploy tool handler.
func NewDeploy(baseDir string, provider DeployProvider, taskID string, registry string, devfleetURL string) *Deploy {
	return &Deploy{
		BaseDir:    baseDir,
		Provider:   provider,
		TaskID:     taskID,
		Registry:   registry,
		DevfleetURL: devfleetURL,
	}
}

func (d *Deploy) dockerCmd(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = d.BaseDir
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		return result, fmt.Errorf("docker %v: %w", args, err)
	}
	return result, nil
}

const (
	defaultDeployPort       = "8080"
	defaultLogTailLines     = "100"
	defaultStopTimeoutSec   = "10"
	defaultBuildTimeoutSec  = 300
	defaultPushTimeoutSec   = 300
	defaultRunDetachTimeout = 120
	defaultRunAttachTimeout = 1800
)

func (d *Deploy) safeTaskID() string {
	return strings.ToLower(strings.ReplaceAll(d.TaskID, "/", "-"))
}

func (d *Deploy) imageBase() string {
	safeID := d.safeTaskID()
	if d.Registry != "" {
		return fmt.Sprintf("%s/devfleet-%s", d.Registry, safeID)
	}
	return fmt.Sprintf("devfleet-%s", safeID)
}

func (d *Deploy) imageTag() string {
	return d.imageBase() + ":latest"
}

func (d *Deploy) containerName() string {
	return fmt.Sprintf("devfleet-%s", d.safeTaskID())
}

func (d *Deploy) resolveContainerName(args map[string]any) string {
	if name := getOptString(args, "name", ""); name != "" {
		return name
	}
	return d.containerName()
}

func (d *Deploy) resolvePort(args map[string]any) string {
	port := getOptString(args, "port", defaultDeployPort)
	if port == "" {
		return defaultDeployPort
	}
	return port
}

func (d *Deploy) gitShortSHA() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = d.BaseDir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("%d", time.Now().Unix())
	}
	return strings.TrimSpace(string(out))
}

func (d *Deploy) resolveImageTag(args map[string]any) string {
	if tag := getOptString(args, "tag", ""); tag != "" {
		return tag
	}
	base := d.imageBase()
	sha := d.gitShortSHA()
	return fmt.Sprintf("%s:%s", base, sha)
}

// Build handles deploy_build: builds a versioned Docker image from the
// workspace. Both a versioned tag and :latest tag are applied.
func (d *Deploy) Build(ctx context.Context, args map[string]any) (any, error) {
	dockerfile := getOptString(args, "dockerfile", "")
	if dockerfile == "" {
		dockerfile = detectDockerfile(d.BaseDir)
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
	}
	contextDir := getOptString(args, "context", ".")
	if contextDir == "" {
		contextDir = "."
	}

	versionedTag := d.resolveImageTag(args)
	latestTag := d.imageTag()

	buildArgs := getOptStringSlice(args, "build_args")
	dockerArgs := []string{"build", "-f", dockerfile, "-t", versionedTag, "-t", latestTag}
	for _, a := range buildArgs {
		if s, ok := a.(string); ok {
			dockerArgs = append(dockerArgs, "--build-arg", s)
		} else {
			slog.Warn("deploy_build: skipping non-string build arg", "value", a)
		}
	}
	dockerArgs = append(dockerArgs, contextDir)

	timeoutSec := getOptFloat64(args, "timeout_sec", defaultBuildTimeoutSec)
	buildCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	out, err := d.dockerCmd(buildCtx, dockerArgs...)
	if err != nil {
		return map[string]any{
			"image": versionedTag,
			"built": false,
			"log":   out,
		}, nil
	}

	return map[string]any{
		"image":       versionedTag,
		"image_latest": latestTag,
		"built":       true,
		"provider":    string(d.Provider),
	}, nil
}

// Push handles deploy_push: pushes the versioned tag and :latest tag
// to the configured container registry.
func (d *Deploy) Push(ctx context.Context, args map[string]any) (any, error) {
	tag := getOptString(args, "image", "")
	if tag == "" {
		tag = d.imageTag()
	}

	latestTag := d.imageTag()

	timeoutSec := getOptFloat64(args, "timeout_sec", defaultPushTimeoutSec)

	// Single parent timeout across both pushes
	pushCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	out, err := d.dockerCmd(pushCtx, "push", tag)
	if err != nil {
		return map[string]any{
			"image":  tag,
			"pushed": false,
			"log":    out,
		}, nil
	}

	pushed := []string{tag}

	latestOut, latestErr := d.dockerCmd(pushCtx, "push", latestTag)
	if latestErr == nil {
		pushed = append(pushed, latestTag)
	} else {
		pushed = append(pushed, fmt.Sprintf("%s (failed: %s)", latestTag, strings.TrimSpace(latestOut)))
	}

	return map[string]any{
		"image":    tag,
		"pushed":   true,
		"versions": pushed,
		"provider": string(d.Provider),
	}, nil
}

// Run handles deploy_run: starts a container from a built image,
// stopping any existing container with the same name first.
func (d *Deploy) Run(ctx context.Context, args map[string]any) (any, error) {
	image := getOptString(args, "image", "")
	if image == "" {
		image = d.imageTag()
	}
	name := d.resolveContainerName(args)
	port := d.resolvePort(args)
	envVars := getOptStringMap(args, "env")
	detach := true
	if detachVal, ok := args["detach"].(bool); ok {
		detach = detachVal
	}

	if out, err := d.dockerCmd(ctx, "stop", "-t", "5", name); err != nil {
		slog.Debug("deploy_run: stop previous container", "name", name, "error", err, "output", out)
	}
	if out, err := d.dockerCmd(ctx, "rm", "-f", name); err != nil {
		slog.Debug("deploy_run: rm previous container", "name", name, "error", err, "output", out)
	}

	dockerArgs := []string{"run", "--name", name}
	if detach {
		dockerArgs = append(dockerArgs, "-d")
	}
	dockerArgs = append(dockerArgs, "-p", port+":"+port)
	for k, v := range envVars {
		dockerArgs = append(dockerArgs, "-e", k+"="+fmt.Sprint(v))
	}
	dockerArgs = append(dockerArgs, image)

	timeoutSec := getOptFloat64(args, "timeout_sec", 0)
	if timeoutSec == 0 {
		if detach {
			timeoutSec = defaultRunDetachTimeout
		} else {
			timeoutSec = defaultRunAttachTimeout
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	out, err := d.dockerCmd(runCtx, dockerArgs...)
	if err != nil {
		return map[string]any{
			"container": name,
			"running":   false,
			"log":       out,
		}, nil
	}

	containerID := strings.TrimSpace(out)

	url := fmt.Sprintf("http://localhost:%s", port)
	host := ""
	if d.Provider == DeployProviderPreview && d.DevfleetURL != "" {
		host = fmt.Sprintf("%s.%s", strings.ReplaceAll(d.TaskID, "/", "-"), d.DevfleetURL)
		url = fmt.Sprintf("https://%s", host)
	}

	return map[string]any{
		"container_id": containerID,
		"container":    name,
		"image":        image,
		"running":      true,
		"port":         port,
		"url":          url,
		"host":         host,
		"provider":     string(d.Provider),
	}, nil
}

// Status handles deploy_status: reports whether a container is running
// and which ports are exposed.
func (d *Deploy) Status(ctx context.Context, args map[string]any) (any, error) {
	name := d.resolveContainerName(args)

	out, err := d.dockerCmd(ctx, "inspect", "--format", "{{json .State}}", name)
	if err != nil {
		if strings.Contains(out, "No such object") || strings.Contains(out, "No such container") {
			return map[string]any{
				"container": name,
				"running":   false,
				"exists":    false,
				"provider":  string(d.Provider),
			}, nil
		}
		return nil, fmt.Errorf("docker inspect: %s", out)
	}

	var state map[string]any
	if err := json.Unmarshal([]byte(out), &state); err != nil {
		return nil, fmt.Errorf("parse container state: %w", err)
	}

	running, ok := state["Running"].(bool)
	if !ok {
		return nil, fmt.Errorf("parse container state: Running is not a boolean")
	}
	status, ok := state["Status"].(string)
	if !ok {
		return nil, fmt.Errorf("parse container state: Status is not a string")
	}

	result := map[string]any{
		"container": name,
		"running":   running,
		"exists":    true,
		"status":    status,
		"provider":  string(d.Provider),
	}

	if running {
		portsOut, err := d.dockerCmd(ctx, "port", name)
		if err == nil {
			result["ports"] = portsOut
		}
	}

	return result, nil
}

// Stop handles deploy_stop: gracefully stops a running container with
// a configurable timeout before force-killing.
func (d *Deploy) Stop(ctx context.Context, args map[string]any) (any, error) {
	name := d.resolveContainerName(args)

	var timeout string
	switch t := args["timeout"].(type) {
	case string:
		timeout = t
	case float64:
		timeout = strconv.FormatInt(int64(t), 10)
	default:
		timeout = defaultStopTimeoutSec
	}
	if timeout == "" {
		timeout = defaultStopTimeoutSec
	}

	out, err := d.dockerCmd(ctx, "stop", "-t", timeout, name)
	if err != nil {
		return map[string]any{
			"container": name,
			"stopped":   false,
			"log":       out,
		}, nil
	}

	return map[string]any{
		"container": name,
		"stopped":   true,
	}, nil
}

// Logs handles deploy_logs: retrieves stdout/stderr from a deployed
// container.
func (d *Deploy) Logs(ctx context.Context, args map[string]any) (any, error) {
	name := d.resolveContainerName(args)
	tail := getOptString(args, "tail", defaultLogTailLines)
	if tail == "" {
		tail = defaultLogTailLines
	}

	out, err := d.dockerCmd(ctx, "logs", "--tail", tail, name)
	if err != nil {
		return nil, fmt.Errorf("docker logs: %s", out)
	}

	return map[string]any{
		"container": name,
		"logs":      out,
	}, nil
}

// ListContainers handles deploy_list: lists all deployment containers
// with name, image, status, and port mappings.
func (d *Deploy) ListContainers(ctx context.Context, args map[string]any) (any, error) {
	all := getOptBool(args, "all", false)

	dockerArgs := []string{"ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}"}
	if all {
		dockerArgs = append(dockerArgs, "-a")
	}

	filter := getOptString(args, "filter", "")
	if filter != "" {
		dockerArgs = append(dockerArgs, "--filter", filter)
	}

	out, err := d.dockerCmd(ctx, dockerArgs...)
	if err != nil {
		return nil, fmt.Errorf("docker ps: %s", out)
	}

	var containers []map[string]string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		entry := map[string]string{
			"name":   parts[0],
			"image":  parts[1],
			"status": parts[2],
		}
		if len(parts) > 3 {
			entry["ports"] = parts[3]
		}
		containers = append(containers, entry)
	}
	if containers == nil {
		containers = []map[string]string{}
	}

	return map[string]any{
		"containers": containers,
		"count":      len(containers),
	}, nil
}

// ListVersions handles deploy_versions: lists all built image versions
// (repository:tag, image ID, creation time, and size).
func (d *Deploy) ListVersions(ctx context.Context, args map[string]any) (any, error) {
	base := d.imageBase()
	dockerArgs := []string{"images", base, "--format", "{{.Repository}}:{{.Tag}}\t{{.ID}}\t{{.CreatedAt}}\t{{.Size}}"}

	out, err := d.dockerCmd(ctx, dockerArgs...)
	if err != nil {
		return nil, fmt.Errorf("docker images: %s", out)
	}

	var versions []map[string]string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		entry := map[string]string{
			"tag": parts[0],
		}
		if len(parts) > 1 {
			entry["image_id"] = parts[1]
		}
		if len(parts) > 2 {
			entry["created"] = parts[2]
		}
		if len(parts) > 3 {
			entry["size"] = parts[3]
		}
		versions = append(versions, entry)
	}
	if versions == nil {
		versions = []map[string]string{}
	}

	return map[string]any{
		"base_image": base,
		"versions":   versions,
		"count":      len(versions),
	}, nil
}

// Rollback handles deploy_rollback: stops the running container and
// starts a replacement from the specified image tag.
func (d *Deploy) Rollback(ctx context.Context, args map[string]any) (any, error) {
	version, err := getString(args, "image")
	if err != nil {
		return nil, err
	}
	if version == "" {
		return nil, fmt.Errorf("image tag is required for rollback (e.g. devfleet-abc123:def4567)")
	}

	name := d.resolveContainerName(args)
	port := d.resolvePort(args)

	stopCtx, stopCancel := context.WithTimeout(ctx, 30*time.Second)
	defer stopCancel()
	d.dockerCmd(stopCtx, "stop", "-t", "5", name)
	d.dockerCmd(stopCtx, "rm", "-f", name)

	runCtx, runCancel := context.WithTimeout(ctx, 120*time.Second)
	defer runCancel()

	dockerArgs := []string{"run", "-d", "--name", name, "-p", port + ":" + port}
	envVars := getOptStringMap(args, "env")
	for k, v := range envVars {
		dockerArgs = append(dockerArgs, "-e", k + "=" + fmt.Sprint(v))
	}
	dockerArgs = append(dockerArgs, version)

	out, err := d.dockerCmd(runCtx, dockerArgs...)
	if err != nil {
		return map[string]any{
			"container": name,
			"image":     version,
			"running":   false,
			"log":       out,
		}, nil
	}

	return map[string]any{
		"container_id": strings.TrimSpace(out),
		"container":    name,
		"image":        version,
		"running":      true,
		"port":         port,
		"provider":     string(d.Provider),
	}, nil
}

func detectDockerfile(baseDir string) string {
	candidates := []string{
		"Dockerfile",
		"docker/Dockerfile",
		".deploy/Dockerfile",
		"deploy/Dockerfile",
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(baseDir, c)); err == nil {
			return c
		}
	}
	return ""
}

