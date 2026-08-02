package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/internal/agentenv"
	"github.com/flatout-works/chetter/runner/internal/task"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	kubernetesAgentPort   = 9999
	kubernetesCleanupWait = 20 * time.Second
	kubernetesDiagWait    = 10 * time.Second
	ownedLabel            = "chetter.io/owned"
	runnerLabel           = "chetter.io/runner-id"
)

func (r *Runner) initializeKubernetesClient() error {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		path := r.cfg.Kubernetes.Kubeconfig
		if path == "" {
			path = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			return fmt.Errorf("initialize kubernetes client: %w", err)
		}
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("initialize kubernetes client: %w", err)
	}
	r.kubeConfig = cfg
	r.kubeClient = client
	return nil
}

func (r *Runner) verifyAndReconcileKubernetes(ctx context.Context) error {
	if r.kubeClient == nil {
		return errors.New("kubernetes client is not configured")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := r.kubeClient.Discovery().ServerVersion(); err != nil {
		return fmt.Errorf("verify kubernetes API: %w", err)
	}
	selector := labels.Set{ownedLabel: "true", runnerLabel: labelValue(r.runnerID)}.AsSelector().String()
	pods, err := r.kubeClient.CoreV1().Pods(r.cfg.Kubernetes.Namespace).List(checkCtx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("list stale kubernetes pods: %w", err)
	}
	grace := int64(0)
	for _, pod := range pods.Items {
		if err := r.kubeClient.CoreV1().Pods(r.cfg.Kubernetes.Namespace).Delete(checkCtx, pod.Name, metav1.DeleteOptions{GracePeriodSeconds: &grace}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale kubernetes pod %s: %w", pod.Name, err)
		}
	}
	secrets, err := r.kubeClient.CoreV1().Secrets(r.cfg.Kubernetes.Namespace).List(checkCtx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("list stale kubernetes secrets: %w", err)
	}
	for _, secret := range secrets.Items {
		if err := r.kubeClient.CoreV1().Secrets(r.cfg.Kubernetes.Namespace).Delete(checkCtx, secret.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale kubernetes secret %s: %w", secret.Name, err)
		}
	}
	return nil
}

func (r *Runner) runnerHostIP() string {
	if ip := strings.TrimSpace(firstEnv("RUNNER_HOST_IP", "POD_IP")); ip != "" {
		return ip
	}
	if r.executionMode() == "kubernetes" {
		return "127.0.0.1"
	}
	return hostIP(runcNetwork())
}

func kubernetesResourceName(runnerID string, req task.TaskRequest) string {
	readable := dnsFragment(req.TaskID)
	if len(readable) > 28 {
		readable = readable[:28]
	}
	sum := sha256.Sum256([]byte(runnerID + "\x00" + req.TaskID + "\x00" + req.ExecutionID))
	return "chetter-" + readable + "-" + hex.EncodeToString(sum[:8])
}

func dnsFragment(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, ch := range value {
		valid := ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9'
		if valid {
			b.WriteRune(ch)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "task"
	}
	return result
}

func labelValue(value string) string {
	if len(validation.IsValidLabelValue(value)) == 0 {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return "id-" + hex.EncodeToString(sum[:12])
}

func (r *Runner) kubernetesLabels(req task.TaskRequest) map[string]string {
	values := map[string]string{
		ownedLabel:                  "true",
		runnerLabel:                 r.runnerID,
		"chetter.io/task-id":        req.TaskID,
		"chetter.io/execution-id":   req.ExecutionID,
		"chetter.io/session-id":     req.AgentSessionID,
		"chetter.io/user-prompt-id": req.UserPromptID,
	}
	for key, value := range values {
		if value == "" {
			delete(values, key)
			continue
		}
		values[key] = labelValue(value)
	}
	return values
}

func (r *Runner) kubernetesOwnerReferences() []metav1.OwnerReference {
	name := strings.TrimSpace(os.Getenv("POD_NAME"))
	uid := strings.TrimSpace(os.Getenv("POD_UID"))
	if name == "" || uid == "" {
		return nil
	}
	controller := true
	blockOwnerDeletion := false
	return []metav1.OwnerReference{{
		APIVersion:         "v1",
		Kind:               "Pod",
		Name:               name,
		UID:                types.UID(uid),
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}}
}

func (r *Runner) kubernetesEnvironment(req task.TaskRequest, workspace, secret string, h harness.Harness) map[string][]byte {
	env := make(map[string]string)
	for key, value := range req.Env {
		if !agentenv.IsManagedEnv(key, req) {
			env[key] = value
		}
	}
	agentenv.AddRunnerOwnedEnv(env)
	for _, value := range agentenv.ProviderCredentialEnv(req) {
		key, value, _ := strings.Cut(value, "=")
		env[key] = value
	}
	for _, endpoint := range req.McpEndpoints {
		if key := strings.TrimSpace(endpoint.BearerTokenEnv); key != "" {
			env[key] = os.Getenv(key)
		}
	}
	for _, value := range agentenv.GitIdentityEnv(req, workspace) {
		key, value, _ := strings.Cut(value, "=")
		env[key] = value
	}
	for key, value := range h.Env(workspace, secret, req) {
		env[key] = value
	}
	env["TASK_ID"] = req.TaskID
	env["WORKSPACE"] = workspace
	env["HOME"] = workspace
	env["XDG_CONFIG_HOME"] = filepath.Join(workspace, ".config")
	env["XDG_DATA_HOME"] = filepath.Join(workspace, ".local/share")
	env["XDG_STATE_HOME"] = filepath.Join(workspace, ".local/state")
	env["XDG_CACHE_HOME"] = filepath.Join(workspace, ".cache")
	env["CHETTER_AGENT_NAME"] = req.Agent
	env["CHETTER_MODEL_ID"] = h.ResolvedModelID(req)
	env["CHETTER_TASK_ID"] = req.TaskID
	env["CHETTER_AGENT_SESSION_ID"] = req.AgentSessionID
	env["CHETTER_USER_PROMPT_ID"] = req.UserPromptID
	env["CHETTER_EXECUTION_ID"] = req.ExecutionID
	env["CHETTER_RUNNER_IMAGE"] = os.Getenv("CHETTER_RUNNER_IMAGE")
	env["CHETTER_RUNNER_IMAGE_DIGEST"] = os.Getenv("CHETTER_RUNNER_IMAGE_DIGEST")
	if host := r.runnerHostIP(); host != "" {
		proxy := "http://" + net.JoinHostPort(host, "18080")
		env["HTTP_PROXY"], env["HTTPS_PROXY"] = proxy, proxy
		env["http_proxy"], env["https_proxy"] = proxy, proxy
		env["CHETTER_PROXY"] = net.JoinHostPort(host, "18080")
		env["NODE_USE_ENV_PROXY"] = "1"
		env["NO_PROXY"], env["no_proxy"] = gvisorNoProxy(), gvisorNoProxy()
	}
	data := make(map[string][]byte, len(env))
	for key, value := range env {
		data[key] = []byte(value)
	}
	return data
}

func workspaceSubPath(root, workspace string) (string, error) {
	root = filepath.Clean(root)
	workspace = filepath.Clean(workspace)
	if !filepath.IsAbs(root) || !filepath.IsAbs(workspace) {
		return "", fmt.Errorf("workspace root and execution workspace must be absolute: root=%q workspace=%q", root, workspace)
	}
	rel, err := filepath.Rel(root, workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace relative to root: %w", err)
	}
	if rel == "." || rel == "" || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace %q must be a child of workspace root %q", workspace, root)
	}
	return rel, nil
}

func (r *Runner) buildKubernetesPod(req task.TaskRequest, workspace, secretName string, command []string, rpc bool) (*corev1.Pod, error) {
	if len(command) == 0 {
		return nil, errors.New("agent command is required")
	}
	name := kubernetesResourceName(r.runnerID, req)
	root := r.cfg.Runner.WorkspaceRoot
	subPath, err := workspaceSubPath(root, workspace)
	if err != nil {
		return nil, err
	}
	falseValue := false
	container := corev1.Container{
		Name:            "agent",
		Image:           req.AgentImage,
		ImagePullPolicy: corev1.PullPolicy(r.cfg.Kubernetes.ImagePullPolicy),
		Command:         command[:1],
		Args:            command[1:],
		WorkingDir:      workspace,
		EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
		}}},
		VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: workspace, SubPath: subPath}},
	}
	if rpc {
		container.Stdin = true
		container.StdinOnce = true
	} else {
		container.Ports = []corev1.ContainerPort{{Name: "harness", ContainerPort: kubernetesAgentPort}}
	}
	if req.MaxMemoryMB > 0 {
		container.Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(strconv.Itoa(req.MaxMemoryMB) + "Mi")}
	}
	if req.MaxCPU > 0 {
		if container.Resources.Limits == nil {
			container.Resources.Limits = corev1.ResourceList{}
		}
		container.Resources.Limits[corev1.ResourceCPU] = resource.MustParse(strconv.Itoa(req.MaxCPU))
	}
	volume := corev1.Volume{Name: "workspace"}
	if r.cfg.Kubernetes.WorkspacePVC != "" {
		volume.PersistentVolumeClaim = &corev1.PersistentVolumeClaimVolumeSource{ClaimName: r.cfg.Kubernetes.WorkspacePVC}
	} else {
		volume.HostPath = &corev1.HostPathVolumeSource{Path: r.cfg.Kubernetes.WorkspaceHostPath, Type: hostPathType(corev1.HostPathDirectoryOrCreate)}
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: r.cfg.Kubernetes.Namespace, Labels: r.kubernetesLabels(req), OwnerReferences: r.kubernetesOwnerReferences()},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: &falseValue,
			ServiceAccountName:           r.cfg.Kubernetes.AgentServiceAccount,
			Containers:                   []corev1.Container{container},
			Volumes:                      []corev1.Volume{volume},
		},
	}
	if runtimeClass := r.cfg.Kubernetes.RuntimeClass; runtimeClass != "" {
		pod.Spec.RuntimeClassName = &runtimeClass
	}
	if r.cfg.Kubernetes.WorkspaceHostPath != "" {
		if r.cfg.Kubernetes.NodeName == "" {
			return nil, errors.New("NODE_NAME is required for kubernetes hostPath mode")
		}
		pod.Spec.NodeName = r.cfg.Kubernetes.NodeName
	}
	return pod, nil
}

func hostPathType(value corev1.HostPathType) *corev1.HostPathType { return &value }

func (r *Runner) createKubernetesAgent(ctx context.Context, req task.TaskRequest, workspace, secret string, h harness.Harness, command []string, rpc bool) (*corev1.Pod, error) {
	name := kubernetesResourceName(r.runnerID, req)
	// A prior runner crash may have left resources for this exact execution.
	if err := r.cleanupKubernetesResources(name); err != nil {
		return nil, fmt.Errorf("remove stale resources for execution: %w", err)
	}
	podSpec, err := r.buildKubernetesPod(req, workspace, name, command, rpc)
	if err != nil {
		return nil, err
	}
	labels := r.kubernetesLabels(req)
	secretObject := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: r.cfg.Kubernetes.Namespace, Labels: labels, OwnerReferences: r.kubernetesOwnerReferences()}, Data: r.kubernetesEnvironment(req, workspace, secret, h)}
	if _, err := r.kubeClient.CoreV1().Secrets(r.cfg.Kubernetes.Namespace).Create(ctx, secretObject, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create agent environment secret: %w", err)
	}
	pod, err := r.kubeClient.CoreV1().Pods(r.cfg.Kubernetes.Namespace).Create(ctx, podSpec, metav1.CreateOptions{})
	if err != nil {
		_ = r.cleanupKubernetesResources(name)
		return nil, fmt.Errorf("create agent pod: %w", err)
	}
	return pod, nil
}

func (r *Runner) waitForKubernetesAgent(ctx context.Context, name string) (*corev1.Pod, error) {
	timeout := time.Duration(r.cfg.Kubernetes.PodReadyTimeoutSec) * time.Second
	var result *corev1.Pod
	err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, timeout, true, func(ctx context.Context) (bool, error) {
		pod, err := r.kubeClient.CoreV1().Pods(r.cfg.Kubernetes.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if failure := kubernetesPodFailure(pod); failure != "" {
			return false, errors.New(failure)
		}
		if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			return false, nil
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == "agent" && status.Ready {
				result = pod
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func kubernetesPodFailure(pod *corev1.Pod) string {
	if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
		return fmt.Sprintf("pod entered %s: %s %s", pod.Status.Phase, pod.Status.Reason, pod.Status.Message)
	}
	statuses := append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
	for _, status := range statuses {
		if waiting := status.State.Waiting; waiting != nil {
			switch waiting.Reason {
			case "ErrImagePull", "ImagePullBackOff", "CreateContainerConfigError", "CreateContainerError", "InvalidImageName", "CrashLoopBackOff":
				return fmt.Sprintf("container %s waiting: %s: %s", status.Name, waiting.Reason, waiting.Message)
			}
		}
		if terminated := status.State.Terminated; terminated != nil && terminated.ExitCode != 0 {
			reason := terminated.Reason
			if reason == "OOMKilled" {
				reason = "OOMKilled (memory limit exceeded)"
			}
			return fmt.Sprintf("container %s terminated with exit %d: %s: %s", status.Name, terminated.ExitCode, reason, terminated.Message)
		}
	}
	return ""
}

func (r *Runner) cleanupKubernetesResources(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), kubernetesCleanupWait)
	defer cancel()
	grace := int64(0)
	var cleanupErrors []error
	if err := r.kubeClient.CoreV1().Pods(r.cfg.Kubernetes.Namespace).Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &grace}); err != nil && !apierrors.IsNotFound(err) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("delete agent pod %s: %w", name, err))
	}
	if err := r.kubeClient.CoreV1().Secrets(r.cfg.Kubernetes.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("delete agent environment secret %s: %w", name, err))
	}
	if err := wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		podGone, err := kubernetesObjectNotFound(func() error {
			_, err := r.kubeClient.CoreV1().Pods(r.cfg.Kubernetes.Namespace).Get(ctx, name, metav1.GetOptions{})
			return err
		})
		if err != nil || !podGone {
			return false, err
		}
		secretGone, err := kubernetesObjectNotFound(func() error {
			_, err := r.kubeClient.CoreV1().Secrets(r.cfg.Kubernetes.Namespace).Get(ctx, name, metav1.GetOptions{})
			return err
		})
		return secretGone, err
	}); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("wait for agent resources %s deletion: %w", name, err))
	}
	return errors.Join(cleanupErrors...)
}

func kubernetesObjectNotFound(get func() error) (bool, error) {
	err := get()
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func (r *Runner) cleanupKubernetesResourcesAndReport(req task.TaskRequest, name string) bool {
	if err := r.cleanupKubernetesResources(name); err != nil {
		slog.Warn("clean up kubernetes agent resources", "taskID", req.TaskID, "name", name, "err", err)
		r.publishEvent(req, fmt.Sprintf("kubernetes cleanup: %v", err))
		return false
	}
	return true
}

func (r *Runner) kubernetesDiagnostics(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), kubernetesDiagWait)
	defer cancel()
	var parts []string
	if pod, err := r.kubeClient.CoreV1().Pods(r.cfg.Kubernetes.Namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
		parts = append(parts, fmt.Sprintf("phase=%s reason=%s message=%s", pod.Status.Phase, pod.Status.Reason, pod.Status.Message))
		for _, condition := range pod.Status.Conditions {
			if condition.Status != corev1.ConditionTrue {
				parts = append(parts, fmt.Sprintf("condition %s: %s: %s", condition.Type, condition.Reason, condition.Message))
			}
		}
	}
	tail := int64(200)
	if logs, err := r.kubeClient.CoreV1().Pods(r.cfg.Kubernetes.Namespace).GetLogs(name, &corev1.PodLogOptions{Container: "agent", TailLines: &tail}).DoRaw(ctx); err == nil && len(logs) > 0 {
		parts = append(parts, "logs: "+string(logs))
	}
	if events, err := r.kubeClient.CoreV1().Events(r.cfg.Kubernetes.Namespace).List(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("involvedObject.name", name).String()}); err == nil {
		sort.Slice(events.Items, func(i, j int) bool {
			return events.Items[i].CreationTimestamp.Before(&events.Items[j].CreationTimestamp)
		})
		for _, event := range events.Items {
			parts = append(parts, fmt.Sprintf("event %s: %s", event.Reason, event.Message))
		}
	}
	return truncateSummary(strings.Join(parts, "\n"))
}

func (r *Runner) runKubernetesAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, h harness.ServeHarness, resume bool) {
	if req.Prompt == "" {
		r.publishStatusForRequest(req, "error", "no prompt provided", nil)
		return
	}
	workspace := session.WorkspaceDir
	secret := h.ServerPassword()
	command := h.ServeCommand(kubernetesAgentPort)
	if len(command) == 0 {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("harness %s does not support serve mode", h.Name()), nil)
		return
	}
	name := kubernetesResourceName(r.runnerID, req)
	if _, err := r.createKubernetesAgent(ctx, req, workspace, secret, h, command, false); err != nil {
		r.publishStatusForRequest(req, "error", err.Error(), nil)
		return
	}
	defer r.cleanupKubernetesResourcesAndReport(req, name)
	r.publishStatusForRequest(req, "running", "Waiting for Kubernetes agent pod...", nil)
	pod, err := r.waitForKubernetesAgent(ctx, name)
	if err != nil {
		diagnostics := r.kubernetesDiagnostics(name)
		r.publishEvent(req, diagnostics)
		r.publishStatusForRequest(req, "error", fmt.Sprintf("agent pod not ready: %v", err), nil)
		return
	}
	baseURL := "http://" + net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(kubernetesAgentPort))
	if err := h.WaitForReady(ctx, baseURL, secret, time.Duration(r.cfg.Kubernetes.PodReadyTimeoutSec)*time.Second); err != nil {
		r.publishEvent(req, r.kubernetesDiagnostics(name))
		r.publishStatusForRequest(req, "error", fmt.Sprintf("pod harness serve not ready: %v", err), nil)
		return
	}
	sid := req.ResumeHarnessSessionID
	if !resume {
		sid, err = h.CreateSession(ctx, baseURL, secret)
		if err != nil {
			r.publishStatusForRequest(req, "error", fmt.Sprintf("create session: %v", err), nil)
			return
		}
	}
	var tokenUsage tokenUsageAccumulator
	agentCtx, stopWatching, watchdog := r.watchHarnessProgress(ctx, h, req, baseURL, sid, secret, workspace, &tokenUsage)
	defer stopWatching()
	if resume {
		r.publishStatusForRequest(req, "running", "Sending follow-up prompt to agent...", nil)
	} else {
		r.publishStatusForRequest(req, "running", "Sending prompt to agent...", nil)
	}
	summary, promptErr := h.SendPrompt(agentCtx, baseURL, sid, secret, req, workspace, taskPromptTimeout(req.TimeoutSec))
	stopWatching()
	if watchdog.isStuck() {
		promptErr = errors.New("stuck harness: no progress")
	}
	stopFinalization := r.startFinalizationHeartbeat(req)
	defer stopFinalization()
	r.publishStatusForRequest(req, "running", "Finalizing task result...", nil)
	if promptErr != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if sid != "" {
			_ = h.AbortSession(cleanupCtx, baseURL, sid, secret)
		}
		cancel()
		status, message := "error", fmt.Sprintf("prompt failed: %v", promptErr)
		if ctx.Err() != nil && !watchdog.isStuck() {
			status, message = cancellationStatus(ctx, h.Name())
		}
		if session.PauseOnDrain {
			status, message = "error", "task timeout during drain; resumable session preserved"
		}
		workspacePath := ""
		category := classifyErrorCategory(status, message)
		if category == "transport_error" {
			r.publishEvent(req, r.kubernetesDiagnostics(name))
		}
		var sessionExport string
		if r.cleanupKubernetesResourcesAndReport(req, name) {
			sessionExport = r.readSessionExport(req, workspace, sid, h)
		}
		if req.CheckpointAfterSuccess && (session.PreserveWorkspace || shouldPreserveWorkspaceOnPromptError(category)) {
			session.PreserveWorkspace = true
			workspacePath = workspace
		}
		r.publishStatusWithMetadataAndCheckpoint(req, status, message, nil, sid, sessionExport, "", workspacePath, tokenUsage.delta())
		return
	}
	workspacePath := ""
	if req.CheckpointAfterSuccess {
		session.PreserveWorkspace = true
		workspacePath = workspace
	}
	var sessionExport string
	if r.cleanupKubernetesResourcesAndReport(req, name) {
		sessionExport = r.readSessionExport(req, workspace, sid, h)
	}
	r.publishStatusWithMetadataAndCheckpoint(req, "done", truncateSummary(summary), nil, sid, sessionExport, "", workspacePath, tokenUsage.delta())
}

func (r *Runner) runKubernetesRpcAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, h harness.RPCHarness) {
	if req.Prompt == "" {
		r.publishStatusForRequest(req, "error", "no prompt provided", nil)
		return
	}
	command := h.RpcCommand(req)
	if len(command) == 0 {
		r.publishStatusForRequest(req, "error", "harness does not provide an RPC command", nil)
		return
	}
	name := kubernetesResourceName(r.runnerID, req)
	if _, err := r.createKubernetesAgent(ctx, req, session.WorkspaceDir, "", h, command, true); err != nil {
		r.publishStatusForRequest(req, "error", err.Error(), nil)
		return
	}
	defer r.cleanupKubernetesResourcesAndReport(req, name)
	r.publishStatusForRequest(req, "running", "Waiting for Kubernetes RPC agent pod...", nil)
	if _, err := r.waitForKubernetesAgent(ctx, name); err != nil {
		r.publishEvent(req, r.kubernetesDiagnostics(name))
		r.publishStatusForRequest(req, "error", fmt.Sprintf("RPC agent pod not ready: %v", err), nil)
		return
	}
	if r.kubeConfig == nil {
		r.publishStatusForRequest(req, "error", "kubernetes REST config is required for RPC attach", nil)
		return
	}
	process, err := newKubernetesRPCProcess(ctx, r.kubeClient, r.kubeConfig, r.cfg.Kubernetes.Namespace, name)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("attach RPC agent pod: %v", err), nil)
		return
	}
	r.runRPCAgentCommand(ctx, session, req, h, process, "")
}

type kubernetesRPCProcess struct {
	ctx          context.Context
	cancel       context.CancelFunc
	executor     remotecommand.Executor
	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	stderrReader *io.PipeReader
	stderrWriter *io.PipeWriter
	done         chan error
}

func newKubernetesRPCProcess(ctx context.Context, client kubernetes.Interface, cfg *rest.Config, namespace, podName string) (*kubernetesRPCProcess, error) {
	request := client.CoreV1().RESTClient().Post().Namespace(namespace).Resource("pods").Name(podName).SubResource("attach")
	request.VersionedParams(&corev1.PodAttachOptions{Container: "agent", Stdin: true, Stdout: true, Stderr: true, TTY: false}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(cfg, "POST", request.URL())
	if err != nil {
		return nil, err
	}
	return newKubernetesRPCProcessWithExecutor(ctx, executor), nil
}

func newKubernetesRPCProcessWithExecutor(ctx context.Context, executor remotecommand.Executor) *kubernetesRPCProcess {
	processCtx, cancel := context.WithCancel(ctx)
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	return &kubernetesRPCProcess{
		ctx: processCtx, cancel: cancel, executor: executor,
		stdinReader: stdinReader, stdinWriter: stdinWriter,
		stdoutReader: stdoutReader, stdoutWriter: stdoutWriter,
		stderrReader: stderrReader, stderrWriter: stderrWriter,
		done: make(chan error, 1),
	}
}

func (p *kubernetesRPCProcess) StdinPipe() (io.WriteCloser, error) { return p.stdinWriter, nil }
func (p *kubernetesRPCProcess) StdoutPipe() (io.ReadCloser, error) { return p.stdoutReader, nil }
func (p *kubernetesRPCProcess) StderrPipe() (io.ReadCloser, error) { return p.stderrReader, nil }
func (p *kubernetesRPCProcess) Start() error {
	go func() {
		err := p.executor.StreamWithContext(p.ctx, remotecommand.StreamOptions{Stdin: p.stdinReader, Stdout: p.stdoutWriter, Stderr: p.stderrWriter})
		_ = p.stdinReader.CloseWithError(err)
		_ = p.stdoutWriter.CloseWithError(err)
		_ = p.stderrWriter.CloseWithError(err)
		p.done <- err
	}()
	return nil
}
func (p *kubernetesRPCProcess) Wait() error { return <-p.done }
func (p *kubernetesRPCProcess) Stop() error {
	p.cancel()
	return p.stdinWriter.Close()
}
