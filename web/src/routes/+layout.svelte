<script lang="ts">
  import "../app.css";
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { page } from "$app/stores";
  import { initAuth, auth, login, logout } from "$lib/stores/auth.svelte";
  import { initTheme, toggleTheme, theme } from "$lib/stores/theme.svelte";
  import { startLiveUpdates, stopLiveUpdates } from "$lib/stores/tasks.svelte";
  import { clearTaskDetail } from "$lib/stores/taskDetail.svelte";
  import {
    Sidebar,
    SidebarItem,
    SidebarGroup,
  } from "flowbite-svelte";

  let { children } = $props();

  onMount(() => {
    initAuth();
    initTheme();
  });

  // Start/stop live updates based on auth state
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
    { href: "/", label: "Dashboard", icon: "dashboard" },
    { href: "/tasks", label: "Tasks", icon: "tasks" },
    { href: "/runners", label: "Runners", icon: "runners" },
    { href: "/triggers", label: "Triggers", icon: "triggers" },
    { href: "/sessions", label: "Sessions", icon: "sessions" },
    { href: "/admin", label: "Admin", icon: "admin" },
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
          <button
            type="submit"
            class="w-full mt-4 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white font-medium rounded-lg transition-colors"
          >
            Sign In
          </button>
        </form>
      </div>
    </div>
  </div>
{:else}
  <div class="min-h-screen bg-gray-50 dark:bg-gray-900 flex">
    <!-- Sidebar -->
    <aside class="w-64 bg-white dark:bg-gray-800 border-r border-gray-200 dark:border-gray-700 flex flex-col">
      <div class="p-4 border-b border-gray-200 dark:border-gray-700">
        <h1 class="text-xl font-bold text-gray-900 dark:text-white">Chetter</h1>
      </div>
      <nav class="flex-1 p-3 space-y-1">
        {#each navItems as item (item.href)}
          <a
            href={resolve(item.href)}
            class="block px-3 py-2 rounded-lg text-sm font-medium transition-colors {$page.url.pathname === item.href
              ? 'bg-blue-50 dark:bg-blue-900/30 text-blue-700 dark:text-blue-400'
              : 'text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700'}"
          >
            {item.label}
          </a>
        {/each}
      </nav>
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
      </div>
    </aside>

    <!-- Main content -->
    <main class="flex-1 overflow-auto">
      {@render children()}
    </main>
  </div>
{/if}
