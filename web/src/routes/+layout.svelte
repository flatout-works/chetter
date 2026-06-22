<script lang="ts">
  import "../app.css";
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { page } from "$app/stores";
  import { initAuth, auth, login, logout } from "$lib/stores/auth.svelte";
  import { initTheme, toggleTheme, theme } from "$lib/stores/theme.svelte";
  import { startLiveUpdates, stopLiveUpdates } from "$lib/stores/tasks.svelte";
  import { clearTaskDetail } from "$lib/stores/taskDetail.svelte";
  import Toast from "$lib/components/Toast.svelte";
  import ConfirmDialog from "$lib/components/ConfirmDialog.svelte";
  import { Button, Sidebar, SidebarGroup, SidebarItem, SidebarWrapper } from "flowbite-svelte";

  let { children } = $props();

  let gitHash = $state<string | null>(null);

  onMount(() => {
    initAuth();
    initTheme();
    fetch("/api/server-info")
      .then((r) => r.json())
      .then((info) => {
        if (info.gitHash && info.gitHash !== "unknown") {
          gitHash = info.gitHash;
        }
      })
      .catch(() => {});
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
      <div class="bg-white dark:bg-gray-800 rounded-lg shadow-md p-8">
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-2">Chetter</h1>
        <p class="text-gray-500 dark:text-gray-400 mb-6">Agent fleet control plane</p>
        {#if authState.error}
          <div class="mb-4 p-3 bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400 rounded-lg text-sm">
            {authState.error}
          </div>
        {/if}
        <form onsubmit={handleLogin}>
          <label for="token" class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
            API Token
          </label>
          <input
            id="token"
            type="password"
            bind:value={token}
            placeholder="Enter your bearer token"
            class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white placeholder-gray-400 focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
          <Button type="submit" color="blue" class="w-full mt-4">Sign In</Button>
        </form>
      </div>
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
          <button
            onclick={toggleTheme}
            class="w-full px-3 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 rounded-lg text-left"
          >
            {$theme === "dark" ? "☀ Light" : "🌙 Dark"}
          </button>
          <button
            onclick={logout}
            class="w-full px-3 py-2 text-sm text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 rounded-lg text-left"
          >
            Sign Out
          </button>
          {#if gitHash}
            <div class="pt-2 text-center text-xs text-gray-400 dark:text-gray-500 font-mono">
              {gitHash}
            </div>
          {/if}
        </div>
      </SidebarWrapper>
    </Sidebar>

    <main class="flex-1 overflow-auto">
      {@render children()}
    </main>

    <Toast />
    <ConfirmDialog />
  </div>
{/if}
