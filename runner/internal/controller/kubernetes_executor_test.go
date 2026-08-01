package controller

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/harness/opencode"
	"github.com/flatout-works/chetter/runner/internal/agentenv"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/task"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/remotecommand"
)

func kubernetesTestRunner() *Runner {
	cleanup := true
	return &Runner{
		cfg: &config.Config{
			Runner:    config.RunnerConfig{WorkspaceRoot: "/var/lib/chetter-runner/workspaces"},
			Execution: config.ExecutionConfig{Backend: "kubernetes"},
			Kubernetes: config.KubernetesConfig{
				Namespace: "chetter", RuntimeClass: "gvisor", ImagePullPolicy: "Always",
				CleanupAfterTask: &cleanup, AgentServiceAccount: "chetter-agent", WorkspacePVC: "runner-workspaces", PodReadyTimeoutSec: 1,
			},
		},
		runnerID:   "runner_test/invalid",
		kubeClient: fake.NewSimpleClientset(),
	}
}

func kubernetesTestRequest() task.TaskRequest {
	return task.TaskRequest{
		TaskID: "task_with_a_very_long_identifier_012345678901234567890123456789", ExecutionID: "exec_123", AgentSessionID: "session_123",
		UserPromptID: "prompt_123", AgentImage: "ghcr.io/flatout-works/chetter-agent-base:main", Agent: "opencode",
		GitAuthorName: "Chetter", GitAuthorEmail: "chetter@example.com", MaxMemoryMB: 768, MaxCPU: 2,
	}
}

func TestNewRunnerRequiresUniqueKubernetesRunnerID(t *testing.T) {
	t.Setenv("RUNNER_ID", "")
	_, err := NewRunner(&config.Config{Execution: config.ExecutionConfig{Backend: "kubernetes"}})
	if err == nil || !regexp.MustCompile(`RUNNER_ID`).MatchString(err.Error()) {
		t.Fatalf("expected RUNNER_ID validation error, got %v", err)
	}
}

func TestKubernetesResourceNameIsSafeAndExecutionUnique(t *testing.T) {
	req := kubernetesTestRequest()
	first := kubernetesResourceName("runner-1", req)
	req.ExecutionID = "exec_124"
	second := kubernetesResourceName("runner-1", req)
	if first == second {
		t.Fatal("resource names must differ by execution ID")
	}
	if len(first) > 63 || !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(first) {
		t.Fatalf("invalid resource name %q", first)
	}
	if otherRunner := kubernetesResourceName("runner-2", req); otherRunner == second {
		t.Fatal("resource names must differ by runner ID")
	}
}

func TestBuildKubernetesPodIsolationVolumeAndResources(t *testing.T) {
	r := kubernetesTestRunner()
	req := kubernetesTestRequest()
	workspace := "/var/lib/chetter-runner/workspaces/task/exec/workspace"
	pod, err := r.buildKubernetesPod(req, workspace, "env-secret", []string{"opencode", "serve"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "gvisor" {
		t.Fatalf("runtimeClassName = %v", pod.Spec.RuntimeClassName)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("agent service account token must not be mounted")
	}
	if pod.Spec.ServiceAccountName != "chetter-agent" {
		t.Fatalf("service account = %q", pod.Spec.ServiceAccountName)
	}
	container := pod.Spec.Containers[0]
	if container.Image != req.AgentImage || container.WorkingDir != workspace {
		t.Fatalf("unexpected agent container: %+v", container)
	}
	if len(container.Env) != 0 || len(container.EnvFrom) != 1 || container.EnvFrom[0].SecretRef.Name != "env-secret" {
		t.Fatalf("environment must come only from Secret: %+v", container.EnvFrom)
	}
	if pod.Spec.Volumes[0].PersistentVolumeClaim == nil || pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "runner-workspaces" {
		t.Fatalf("workspace PVC missing: %+v", pod.Spec.Volumes[0])
	}
	mount := container.VolumeMounts[0]
	if mount.MountPath != workspace || mount.SubPath != "task/exec/workspace" {
		t.Fatalf("workspace mount = %+v", mount)
	}
	if mount.MountPath == r.cfg.Runner.WorkspaceRoot {
		t.Fatal("agent Pod exposes sibling execution workspaces")
	}
	if got := container.Resources.Limits.Memory().String(); got != "768Mi" {
		t.Fatalf("memory limit = %q", got)
	}
	if got := container.Resources.Limits.Cpu().String(); got != "2" {
		t.Fatalf("CPU limit = %q", got)
	}
	for _, key := range []string{"chetter.io/task-id", "chetter.io/execution-id", "chetter.io/session-id", "chetter.io/user-prompt-id", runnerLabel} {
		if pod.Labels[key] == "" {
			t.Errorf("missing label %s", key)
		}
	}
}

func TestBuildKubernetesPodRejectsWorkspaceOutsideExecutionRoot(t *testing.T) {
	r := kubernetesTestRunner()
	req := kubernetesTestRequest()
	for _, workspace := range []string{r.cfg.Runner.WorkspaceRoot, "/var/lib/chetter-runner/other/workspace", "/tmp/workspace"} {
		t.Run(workspace, func(t *testing.T) {
			if _, err := r.buildKubernetesPod(req, workspace, "env-secret", []string{"opencode", "serve"}, false); err == nil {
				t.Fatalf("accepted unsafe workspace %q", workspace)
			}
		})
	}
}

func TestBuildKubernetesHostPathPodPinsRunnerNode(t *testing.T) {
	r := kubernetesTestRunner()
	r.cfg.Kubernetes.WorkspacePVC = ""
	r.cfg.Kubernetes.WorkspaceHostPath = r.cfg.Runner.WorkspaceRoot
	r.cfg.Kubernetes.NodeName = "k3s-node-1"
	pod, err := r.buildKubernetesPod(kubernetesTestRequest(), r.cfg.Runner.WorkspaceRoot+"/task/exec/workspace", "env-secret", []string{"opencode", "serve"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if pod.Spec.NodeName != "k3s-node-1" || pod.Spec.Volumes[0].HostPath == nil {
		t.Fatalf("hostPath pod is not pinned: %+v", pod.Spec)
	}
	r.cfg.Kubernetes.NodeName = ""
	if _, err := r.buildKubernetesPod(kubernetesTestRequest(), r.cfg.Runner.WorkspaceRoot+"/task/exec/workspace", "env-secret", []string{"opencode", "serve"}, false); err == nil {
		t.Fatal("hostPath pod accepted without NODE_NAME")
	}
}

func TestKubernetesEnvironmentManagedValuesWin(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "runner-key")
	t.Setenv("RUNNER_HOST_IP", "10.0.0.8")
	r := kubernetesTestRunner()
	req := kubernetesTestRequest()
	req.GitHubCredentialURL = "http://10.0.0.8/internal/github-credential"
	req.GitHubCredentialToken = "execution-capability"
	req.Env = map[string]string{"OPENAI_API_KEY": "task-key", "CUSTOM": "task-value", "CHETTER_EXECUTION_ID": "wrong", agentenv.GitHubCredentialTokenEnv: "attacker-capability"}
	data := r.kubernetesEnvironment(req, "/workspace", "server-secret", opencode.New())
	if string(data["OPENAI_API_KEY"]) != "runner-key" || string(data["CHETTER_EXECUTION_ID"]) != req.ExecutionID {
		t.Fatal("task environment overrode managed values")
	}
	if string(data["CUSTOM"]) != "task-value" || string(data["OPENCODE_SERVER_PASSWORD"]) != "server-secret" {
		t.Fatal("task or harness environment missing")
	}
	if string(data["HTTP_PROXY"]) != "http://10.0.0.8:18080" {
		t.Fatalf("proxy = %q", data["HTTP_PROXY"])
	}
	if string(data[agentenv.GitHubCredentialURLEnv]) != req.GitHubCredentialURL || string(data[agentenv.GitHubCredentialTokenEnv]) != req.GitHubCredentialToken {
		t.Fatal("Kubernetes credential bridge environment is missing or overridden")
	}
}

func TestCreateAndCleanupKubernetesAgentResources(t *testing.T) {
	t.Setenv("POD_NAME", "runner-pod-1")
	t.Setenv("POD_UID", "runner-pod-uid-1")
	r := kubernetesTestRunner()
	req := kubernetesTestRequest()
	name := kubernetesResourceName(r.runnerID, req)
	if _, err := r.createKubernetesAgent(context.Background(), req, "/var/lib/chetter-runner/workspaces/task/exec/workspace", "secret", opencode.New(), []string{"opencode", "serve"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.kubeClient.CoreV1().Pods("chetter").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("pod missing: %v", err)
	}
	secret, err := r.kubeClient.CoreV1().Secrets("chetter").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil || string(secret.Data["CHETTER_EXECUTION_ID"]) != req.ExecutionID {
		t.Fatalf("environment secret missing or wrong: %v", err)
	}
	if len(secret.OwnerReferences) != 1 || string(secret.OwnerReferences[0].UID) != "runner-pod-uid-1" {
		t.Fatalf("secret owner reference = %+v", secret.OwnerReferences)
	}
	pod, err := r.kubeClient.CoreV1().Pods("chetter").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pod.OwnerReferences) != 1 || string(pod.OwnerReferences[0].UID) != "runner-pod-uid-1" {
		t.Fatalf("pod owner reference = %+v", pod.OwnerReferences)
	}
	if err := r.cleanupKubernetesResources(name); err != nil {
		t.Fatal(err)
	}
	if _, err := r.kubeClient.CoreV1().Pods("chetter").Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("pod was not cleaned up")
	}
	if _, err := r.kubeClient.CoreV1().Secrets("chetter").Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("secret was not cleaned up")
	}
}

func TestCleanupWaitsForDelayedPodDeletion(t *testing.T) {
	r := kubernetesTestRunner()
	client := r.kubeClient.(*fake.Clientset)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "delayed", Namespace: "chetter"}}
	if _, err := client.CoreV1().Pods("chetter").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	client.Fake.PrependReactor("delete", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deleteAction := action.(k8stesting.DeleteAction)
		go func() {
			time.Sleep(150 * time.Millisecond)
			_ = client.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "chetter", deleteAction.GetName())
		}()
		return true, nil, nil
	})
	started := time.Now()
	if err := r.cleanupKubernetesResources("delayed"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 100*time.Millisecond {
		t.Fatalf("cleanup returned before Pod deletion was observed: %v", elapsed)
	}
}

func TestWaitForKubernetesAgentReadyAndFailure(t *testing.T) {
	r := kubernetesTestRunner()
	req := kubernetesTestRequest()
	name := kubernetesResourceName(r.runnerID, req)
	pod, err := r.buildKubernetesPod(req, r.cfg.Runner.WorkspaceRoot+"/task/exec/workspace", name, []string{"opencode", "serve"}, false)
	if err != nil {
		t.Fatal(err)
	}
	pod.Status = corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.42.0.9", ContainerStatuses: []corev1.ContainerStatus{{Name: "agent", Ready: true}}}
	if _, err := r.kubeClient.CoreV1().Pods("chetter").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ready, err := r.waitForKubernetesAgent(ctx, name)
	if err != nil || ready.Status.PodIP != "10.42.0.9" {
		t.Fatalf("ready pod = %+v, err=%v", ready, err)
	}

	failure := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "failed", Namespace: "chetter"}, Status: corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{{Name: "agent", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "denied"}}}}}}
	if _, err := r.kubeClient.CoreV1().Pods("chetter").Create(context.Background(), failure, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.waitForKubernetesAgent(ctx, "failed"); err == nil || !regexp.MustCompile(`ImagePullBackOff`).MatchString(err.Error()) {
		t.Fatalf("expected image pull failure, got %v", err)
	}
}

type failingRemoteExecutor struct{ err error }

func (e failingRemoteExecutor) Stream(remotecommand.StreamOptions) error { return e.err }
func (e failingRemoteExecutor) StreamWithContext(context.Context, remotecommand.StreamOptions) error {
	return e.err
}

func TestKubernetesRPCProcessEarlyAttachFailureUnblocksStdin(t *testing.T) {
	wantErr := errors.New("attach failed")
	process := newKubernetesRPCProcessWithExecutor(context.Background(), failingRemoteExecutor{err: wantErr})
	stdin, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := stdin.Write([]byte("ready probe\n"))
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if !errors.Is(err, wantErr) {
			t.Fatalf("stdin write error = %v, want %v", err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("stdin write remained blocked after attach failed")
	}
	if err := process.Wait(); !errors.Is(err, wantErr) {
		t.Fatalf("Wait error = %v, want %v", err, wantErr)
	}
}

func TestVerifyAndReconcileKubernetesOnlyDeletesOwnedRunnerResources(t *testing.T) {
	r := kubernetesTestRunner()
	owned := map[string]string{ownedLabel: "true", runnerLabel: labelValue(r.runnerID)}
	other := map[string]string{ownedLabel: "true", runnerLabel: "another-runner"}
	objects := []*corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "owned", Namespace: "chetter", Labels: owned}},
		{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "chetter", Labels: other}},
	}
	for _, object := range objects {
		if _, err := r.kubeClient.CoreV1().Pods("chetter").Create(context.Background(), object, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.kubeClient.CoreV1().Secrets("chetter").Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "owned", Namespace: "chetter", Labels: owned}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := r.verifyAndReconcileKubernetes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.kubeClient.CoreV1().Pods("chetter").Get(context.Background(), "owned", metav1.GetOptions{}); err == nil {
		t.Fatal("owned stale pod remains")
	}
	if _, err := r.kubeClient.CoreV1().Secrets("chetter").Get(context.Background(), "owned", metav1.GetOptions{}); err == nil {
		t.Fatal("owned stale secret remains")
	}
	if _, err := r.kubeClient.CoreV1().Pods("chetter").Get(context.Background(), "other", metav1.GetOptions{}); err != nil {
		t.Fatalf("another runner's pod was deleted: %v", err)
	}
}
