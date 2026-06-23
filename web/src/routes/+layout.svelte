<script lang="ts">
  import "../app.css";
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { page } from "$app/stores";
  import { initAuth, auth, login, logout } from "$lib/stores/auth.svelte";
  import { initTheme, toggleTheme, theme } from "$lib/stores/theme.svelte";
  import { startLiveUpdates, stopLiveUpdates } from "$lib/stores/tasks.svelte";
  import { clearTaskDetail } from "$lib/stores/taskDetail.svelte";
  import { startServerInfoPolling, stopServerInfoPolling, getServerInfo } from "$lib/stores/serverInfo.svelte";
  import Toast from "$lib/components/Toast.svelte";
  import ConfirmDialog from "$lib/components/ConfirmDialog.svelte";
  import { Alert, Button, Card, Input, Label, Sidebar, SidebarGroup, SidebarItem, SidebarWrapper } from "flowbite-svelte";

  let { children } = $props();

  let serverInfo = $derived(getServerInfo());
  const webGitHash = __WEB_GIT_HASH__;

  onMount(() => {
    initAuth();
    initTheme();
    startServerInfoPolling();
  });

  $effect(() => {
    if (!$auth.authenticated) {
      stopServerInfoPolling();
    }
  });

  let lastAuthed = false;
  auth.subscribe((state) => {
    if (state.authenticated && !lastAuthed) {
      startLiveUpdates();
    } else if (!state.authenticated && lastAuthed) {
      stopLiveUpdates();
      clearTaskDetail();
    }
    lastAuthed = state.authenticated;
  });

  const navItems = [
    { href: "/", label: "Dashboard" },
    { href: "/tasks", label: "Tasks" },
    { href: "/runners", label: "Runners" },
    { href: "/triggers", label: "Triggers" },
    { href: "/sessions", label: "Sessions" },
    { href: "/admin/artifacts", label: "Artifacts" },
    { href: "/admin/audit", label: "Audit Log" },
    { href: "/admin", label: "Admin" },
  ] as const;

  let token = $state("");

  function handleLogin(e: Event) {
    e.preventDefault();
    if (token.trim()) {
      login(token.trim());
      token = "";
    }
  }

  let authState = $derived($auth);

  let activePath = $derived($page.url.pathname);
</script>

{#if !authState.authenticated}
  <div class="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-900">
    <div class="w-full max-w-md">
      <Card size="lg" shadow="md" class="!p-6">
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-2">Chetter</h1>
        <p class="text-gray-500 dark:text-gray-400 mb-6">Agent fleet control plane</p>
        {#if authState.error}
          <Alert color="red" class="mb-4">
            {authState.error}
          </Alert>
        {/if}
        <form onsubmit={handleLogin}>
          <Label for="token" class="mb-2">API Token</Label>
          <Input
            id="token"
            type="password"
            bind:value={token}
            placeholder="Enter your bearer token"
          />
          <Button type="submit" color="blue" class="w-full mt-4">Sign In</Button>
        </form>
      </Card>
    </div>
  </div>
{:else}
  <div class="min-h-screen bg-gray-50 dark:bg-gray-900 flex">
    <Sidebar activeUrl={activePath} position="static" alwaysOpen={true} activateClickOutside={false} backdrop={false} class="w-64 bg-white dark:bg-gray-800 border-r border-gray-200 dark:border-gray-700 flex flex-col h-screen sticky top-0">
      <SidebarWrapper class="flex flex-col h-full">
        <div class="p-4 border-b border-gray-200 dark:border-gray-700">
          <h1 class="text-xl font-bold text-gray-900 dark:text-white">Chetter</h1>
        </div>
        <SidebarGroup border={false} class="flex-1 overflow-y-auto px-3 py-2">
          {#each navItems as item (item.href)}
            <SidebarItem href={resolve(item.href)} label={item.label} />
          {/each}
        </SidebarGroup>
        <div class="p-3 border-t border-gray-200 dark:border-gray-700 space-y-2">
          <Button
            onclick={toggleTheme}
            color="alternative"
            size="sm"
            class="w-full justify-start"
          >
            {$theme === "dark" ? "☀ Light" : "🌙 Dark"}
          </Button>
          <Button
            onclick={logout}
            color="red"
            size="sm"
            class="w-full justify-start"
          >
            Sign Out
          </Button>
          {#if webGitHash !== "unknown" || serverInfo.gitHash}
            <div class="space-y-1 pt-2 text-center text-[11px] font-mono text-gray-400 dark:text-gray-500">
              {#if webGitHash !== "unknown"}
                <div>web {webGitHash}</div>
              {/if}
              {#if serverInfo.gitHash}
                <div>server {serverInfo.gitHash}</div>
              {/if}
            </div>
          {/if}
        </div>
      </SidebarWrapper>
    </Sidebar>

    <main class="flex-1 overflow-auto text-slate-900 dark:text-slate-100">
      {#if serverInfo.quotaExhausted}
        <div class="sticky top-0 z-50 bg-red-50 dark:bg-red-900/30 border-b border-red-200 dark:border-red-800 px-6 py-3 text-sm text-red-700 dark:text-red-300 flex items-center gap-2">
          <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z"/></svg>
          <span>Database quota exhausted — the TiDB cluster has restricted access. <a href="https://docs.pingcap.com/tidbcloud/serverless-limitations#usage-quota" target="_blank" rel="noopener noreferrer" class="underline font-medium">Increase spending limits</a> to restore full functionality.</span>
        </div>
      {/if}
      {@render children()}
    </main>

    <Toast />
    <ConfirmDialog />
  </div>
{/if}
