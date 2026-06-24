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
  import { Alert, Button, Card, Input, Label, Sidebar, SidebarGroup, SidebarItem, SidebarWrapper, SidebarButton } from "flowbite-svelte";

  let { children } = $props();

  let serverInfo = $derived(getServerInfo());
  const webGitHash = __WEB_GIT_HASH__;

  let sidebarOpen = $state(false);

  function closeSidebar() { sidebarOpen = false; }

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
    { href: "/", label: "Dashboard", icon: "M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" },
    { href: "/tasks", label: "Tasks", icon: "M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-6 9l2 2 4-4" },
    { href: "/runners", label: "Runners", icon: "M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" },
    { href: "/triggers", label: "Triggers", icon: "M13 10V3L4 14h7v7l9-11h-7z" },
    { href: "/sessions", label: "Sessions", icon: "M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z" },
    { href: "/admin/artifacts", label: "Artifacts", icon: "M7 21a4 4 0 01-4-4V5a2 2 0 012-2h4a2 2 0 012 2v12a4 4 0 01-4 4zm0 0h12a2 2 0 002-2v-4a2 2 0 00-2-2h-2.343M11 7.343l1.657-1.657a2 2 0 012.828 0l2.829 2.829a2 2 0 010 2.828l-8.486 8.485M7 17h.01" },
    { href: "/admin/audit", label: "Audit Log", icon: "M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-3 7h3m-3 4h3m-6-4h.01M9 16h.01" },
    { href: "/admin", label: "Admin", icon: "M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z M15 12a3 3 0 11-6 0 3 3 0 016 0z" },
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

  let activePathUpstream = $derived(activePath);
  $effect(() => {
    if (activePathUpstream) sidebarOpen = false;
  });
</script>

{#snippet sidebarInner()}
  <SidebarWrapper class="flex flex-col h-full">
    <div class="p-4 border-b border-gray-200 dark:border-gray-700">
      <h1 class="text-xl font-bold text-gray-900 dark:text-white">Chetter</h1>
    </div>
    <SidebarGroup border={false} class="flex-1 overflow-y-auto px-3 py-2">
      {#each navItems as item (item.href)}
        <SidebarItem href={resolve(item.href)} label={item.label}>
          {#snippet icon()}
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d={item.icon} />
            </svg>
          {/snippet}
        </SidebarItem>
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
{/snippet}

{#if !authState.authenticated}
  <div class="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-900 px-4">
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

    <div class="hidden md:block w-64 flex-shrink-0">
      <Sidebar activeUrl={activePath} position="static" alwaysOpen={true} activateClickOutside={false} backdrop={false}
               class="h-screen sticky top-0 bg-white dark:bg-gray-800 border-r border-gray-200 dark:border-gray-700">
        {@render sidebarInner()}
      </Sidebar>
    </div>

    {#if sidebarOpen}
      <button class="md:hidden fixed inset-0 z-30 bg-gray-900/50 dark:bg-black/60 cursor-default border-0 p-0"
              onclick={() => sidebarOpen = false}
              aria-label="Close sidebar"
              type="button"></button>
      <Sidebar activeUrl={activePath} position="static" alwaysOpen={true} activateClickOutside={false} backdrop={false}
               class="md:hidden fixed inset-y-0 left-0 z-40 w-64 bg-white dark:bg-gray-800 border-r border-gray-200 dark:border-gray-700">
        {@render sidebarInner()}
      </Sidebar>
    {/if}

    <div class="flex-1 flex flex-col min-w-0 min-h-screen">
      <div class="md:hidden sticky top-0 z-20 flex items-center gap-3 bg-white dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700 px-4 py-3">
        <SidebarButton onclick={() => sidebarOpen = true} />
        <h1 class="text-lg font-bold text-gray-900 dark:text-white">Chetter</h1>
      </div>

      {#if serverInfo.quotaExhausted}
        <Alert color="red" class="sticky top-0 md:top-0 z-50 rounded-none border-0">
          <div class="flex items-center gap-2">
            <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z"/></svg>
            <span>Database quota exhausted — the TiDB cluster has restricted access. <a href="https://docs.pingcap.com/tidbcloud/serverless-limitations#usage-quota" target="_blank" rel="noopener noreferrer" class="underline font-medium">Increase spending limits</a> to restore full functionality.</span>
          </div>
        </Alert>
      {/if}

      <main class="flex-1 overflow-auto text-slate-900 dark:text-slate-100">
        {@render children()}
      </main>
    </div>

    <Toast />
    <ConfirmDialog />
  </div>
{/if}
