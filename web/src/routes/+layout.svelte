<script lang="ts">
  import "../app.css";
  import { onMount } from "svelte";
  import { base, resolve } from "$app/paths";
  import { page } from "$app/stores";
  import { initAuth, auth, logout } from "$lib/stores/auth.svelte";
  import { initTheme, toggleTheme, theme } from "$lib/stores/theme.svelte";
  import { initSettings } from "$lib/stores/settings.svelte";
  import { startLiveUpdates, stopLiveUpdates } from "$lib/stores/tasks.svelte";
  import { clearTaskDetail } from "$lib/stores/taskDetail.svelte";
  import { startServerInfoPolling, stopServerInfoPolling, getServerInfo } from "$lib/stores/serverInfo.svelte";
  import Toast from "$lib/components/Toast.svelte";
  import ConfirmDialog from "$lib/components/ConfirmDialog.svelte";
  import { Alert, Button, Card, Sidebar, SidebarGroup, SidebarItem, SidebarWrapper, SidebarButton } from "flowbite-svelte";

  let { children } = $props();

  let serverInfo = $derived(getServerInfo());
  const webGitHash = __WEB_GIT_HASH__;

  let sidebarOpen = $state(false);
  let sidebarCollapsed = $state(false);

  let pageUrls = $state(new Map<string, string>());

  $effect(() => {
    pageUrls.set(activePath, $page.url.href);
  });

  function closeSidebar() { sidebarOpen = false; }
  function toggleCollapsed() { sidebarCollapsed = !sidebarCollapsed; }

  onMount(() => {
    initAuth();
    initSettings();
    initTheme();
  });

  $effect(() => {
    if ($auth.authenticated) {
      startServerInfoPolling();
    } else {
      stopServerInfoPolling();
    }
  });

  const navItems = [
    { href: "/", label: "Dashboard", icon: "M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" },
    { href: "/tasks", label: "Tasks", icon: "M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-6 9l2 2 4-4" },
    { href: "/runners", label: "Runners", icon: "M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" },
    { href: "/triggers", label: "Triggers", icon: "M13 10V3L4 14h7v7l9-11h-7z" },
    { href: "/sessions", label: "Sessions", icon: "M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z" },
    { href: "/admin/artifacts", label: "Artifacts", icon: "M7 21a4 4 0 01-4-4V5a2 2 0 012-2h4a2 2 0 012 2v12a4 4 0 01-4 4zm0 0h12a2 2 0 002-2v-4a2 2 0 00-2-2h-2.343M11 7.343l1.657-1.657a2 2 0 012.828 0l2.829 2.829a2 2 0 010 2.828l-8.486 8.485M7 17h.01" },
    { href: "/admin/audit", label: "Audit Log", icon: "M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-3 7h3m-3 4h3m-6-4h.01M9 16h.01" },
    { href: "/settings", label: "Settings", icon: "M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z M15 12a3 3 0 11-6 0 3 3 0 016 0z" },
    { href: "/admin", label: "Admin", icon: "M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z M15 12a3 3 0 11-6 0 3 3 0 016 0z" },
  ] as const;

  const communityLinks = [
    {
      href: "https://github.com/flatout-works/chetter",
      label: "GitHub",
      action: "View on GitHub",
      description: "Source, issues, and releases",
      icon: "M12 2C6.477 2 2 6.59 2 12.253c0 4.526 2.865 8.357 6.839 9.709.5.094.683-.221.683-.489 0-.242-.009-.883-.014-1.733-2.782.617-3.369-1.367-3.369-1.367-.455-1.181-1.11-1.496-1.11-1.496-.908-.635.069-.622.069-.622 1.004.072 1.532 1.052 1.532 1.052.892 1.56 2.341 1.11 2.91.849.091-.659.349-1.11.635-1.365-2.221-.258-4.555-1.133-4.555-5.042 0-1.114.39-2.024 1.029-2.737-.103-.258-.446-1.295.098-2.699 0 0 .84-.274 2.75 1.046A9.385 9.385 0 0112 6.994c.85.004 1.705.117 2.504.345 1.909-1.32 2.747-1.046 2.747-1.046.546 1.404.203 2.441.1 2.699.64.713 1.028 1.623 1.028 2.737 0 3.919-2.338 4.781-4.566 5.034.359.316.678.939.678 1.893 0 1.366-.012 2.469-.012 2.804 0 .271.18.588.688.488C21.139 20.607 24 16.777 24 12.253 24 6.59 19.523 2 14 2h-2z",
    },
    {
      href: "https://discord.gg/KkZxKwSTvF",
      label: "Discord",
      action: "Join Discord",
      description: "Ask questions and follow development",
      icon: "M20.317 4.369A19.791 19.791 0 0015.344 2.8a13.79 13.79 0 00-.637 1.318 18.27 18.27 0 00-5.414 0A13.79 13.79 0 008.656 2.8a19.736 19.736 0 00-4.977 1.572C.533 9.116-.32 13.741.106 18.301a19.9 19.9 0 006.103 3.101 14.72 14.72 0 001.307-2.147 12.94 12.94 0 01-2.06-.994c.173-.129.342-.263.505-.402a14.194 14.194 0 0012.078 0c.165.139.334.273.507.402-.66.393-1.35.727-2.064.996a14.63 14.63 0 001.307 2.145 19.862 19.862 0 006.105-3.101c.5-5.291-.854-9.874-3.577-13.932zM8.02 15.496c-1.188 0-2.164-1.116-2.164-2.489 0-1.372.957-2.489 2.164-2.489 1.214 0 2.183 1.127 2.164 2.489 0 1.373-.957 2.489-2.164 2.489zm7.96 0c-1.188 0-2.164-1.116-2.164-2.489 0-1.372.957-2.489 2.164-2.489 1.214 0 2.183 1.127 2.164 2.489 0 1.373-.95 2.489-2.164 2.489z",
    },
  ] as const;

  const screenshots = [
    {
      src: "/screenshots/screenshot-2.png",
      title: "Fleet dashboard",
      description: "Track task throughput, live status, agents, timing, and runner health from the control plane.",
    },
    {
      src: "/screenshots/screenshot-3.png",
      title: "Automation triggers",
      description: "Manage cron jobs, issue responders, and GitHub PR review workflows from one table.",
    },
    {
      src: "/screenshots/screenshot-1.png",
      title: "Operational view",
      description: "Inspect recent work and drill into agent-created outcomes without leaving the web UI.",
    },
  ] as const;

  let authState = $derived($auth);

  let activePath = $derived($page.url.pathname);

  let needsLiveUpdates = $derived(
    authState.authenticated &&
    (activePath === "/" || activePath === "/tasks")
  );

  $effect(() => {
    if (needsLiveUpdates) {
      startLiveUpdates();
    } else {
      stopLiveUpdates();
      if (!authState.authenticated) clearTaskDetail();
    }
  });

  $effect(() => {
    if (activePath) sidebarOpen = false;
  });

  let isActiveLink = (href: string): boolean => {
    if (href === "/") return activePath === "/";
    return activePath.startsWith(href);
  };

  function navHref(href: string): string {
    if (activePath === href) return $page.url.href;
    return pageUrls.get(href) || href;
  }
</script>

{#snippet sidebarInner()}
  <SidebarWrapper class="flex flex-col h-full">
    <div class="flex items-center p-4 border-b border-gray-200 dark:border-gray-700 gap-2">
      {#if sidebarCollapsed}
        <span class="text-xl font-bold text-gray-900 dark:text-white flex-1 text-center">C</span>
      {:else}
        <h1 class="text-xl font-bold text-gray-900 dark:text-white flex-1">Chetter</h1>
        <button onclick={toggleCollapsed} title="Collapse sidebar"
                class="p-1 rounded-sm text-gray-400 hover:bg-gray-100 dark:text-gray-500 dark:hover:bg-gray-700 shrink-0"
        >
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 19l-7-7 7-7m8 14l-7-7 7-7" />
          </svg>
        </button>
      {/if}
    </div>
    {#if sidebarCollapsed}
      <div class="px-1 py-1 border-b border-gray-200 dark:border-gray-700">
        <button onclick={toggleCollapsed} title="Expand sidebar"
                class="flex items-center justify-center w-full py-2 rounded-sm text-gray-400 hover:bg-gray-100 dark:text-gray-500 dark:hover:bg-gray-700"
        >
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 5l7 7-7 7M5 5l7 7-7 7" />
          </svg>
        </button>
      </div>
    {/if}
    <SidebarGroup border={false} class={sidebarCollapsed ? "flex-1 overflow-y-auto px-1 py-2" : "flex-1 overflow-y-auto px-3 py-2"}>
      {#if sidebarCollapsed}
        {#each navItems as item (item.href)}
             <a href={resolve(item.href)} title={item.label}
             class="flex items-center justify-center py-2.5 rounded-sm mb-0.5 text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700"
             class:bg-gray-200={isActiveLink(item.href)}
             class:dark:bg-gray-700={isActiveLink(item.href)}
             class:text-gray-900={isActiveLink(item.href)}
             class:dark:text-white={isActiveLink(item.href)}
          >
            <svg class="w-5 h-5 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d={item.icon} />
            </svg>
          </a>
        {/each}
      {:else}
        {#each navItems as item (item.href)}
          <SidebarItem href={navHref(item.href)} label={item.label}>
            {#snippet icon()}
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d={item.icon} />
              </svg>
            {/snippet}
          </SidebarItem>
        {/each}
      {/if}
    </SidebarGroup>
    <div class="p-2 border-t border-gray-200 dark:border-gray-700 space-y-1">
      {#if sidebarCollapsed}
        <button onclick={toggleTheme} title={$theme === "dark" ? "Light mode" : "Dark mode"}
                class="flex items-center justify-center w-full py-2.5 rounded-sm text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="{$theme === "dark" ? "M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" : "M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z"}" />
          </svg>
        </button>
        <button onclick={logout} title="Sign Out"
                class="flex items-center justify-center w-full py-2.5 rounded-sm text-red-500 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/30"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" />
          </svg>
        </button>
      {:else}
        <Button onclick={toggleTheme} color="alternative" size="sm" class="w-full justify-start">
          {$theme === "dark" ? "☀ Light" : "🌙 Dark"}
        </Button>
        <Button onclick={logout} color="red" size="sm" class="w-full justify-start">Sign Out</Button>
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
      {/if}
    </div>
  </SidebarWrapper>
{/snippet}

{#if !authState.authenticated}
  <div class="relative min-h-screen overflow-hidden bg-slate-950 px-4 py-8 text-white sm:px-6 lg:px-8">
    <div class="absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(56,189,248,0.25),transparent_34%),radial-gradient(circle_at_bottom_right,rgba(99,102,241,0.22),transparent_35%)]"></div>
    <div class="absolute inset-x-0 top-0 h-px bg-linear-to-r from-transparent via-cyan-300/60 to-transparent"></div>

    <div class="relative mx-auto w-full max-w-7xl">
      <header class="flex flex-col gap-4 py-3 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex items-center gap-3">
          <div class="flex h-11 w-11 items-center justify-center rounded-2xl bg-white text-xl font-black text-slate-950 shadow-xl shadow-cyan-950/30">C</div>
          <div>
            <p class="text-lg font-black tracking-tight">Chetter</p>
            <p class="text-sm text-slate-400">Open source agent control plane</p>
          </div>
        </div>
        <div class="flex flex-col gap-2 sm:flex-row" aria-label="Project links">
          {#each communityLinks as link (link.href)}
            <Button href={link.href} target="_blank" rel="noopener noreferrer" color={link.label === "Discord" ? "purple" : "light"} class="justify-center gap-2">
              <svg class="h-5 w-5" fill="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                <path d={link.icon} />
              </svg>
              {link.action}
            </Button>
          {/each}
        </div>
      </header>

      <section class="grid gap-10 py-12 lg:grid-cols-[0.9fr_1.1fr] lg:items-center lg:py-20">
        <div class="space-y-8">
          <div class="inline-flex items-center gap-2 rounded-full border border-cyan-300/25 bg-cyan-300/10 px-4 py-2 text-sm font-medium text-cyan-100 shadow-lg shadow-cyan-950/40">
            <span class="h-2 w-2 rounded-full bg-emerald-300 shadow-[0_0_16px_rgba(110,231,183,0.8)]"></span>
            Self-hosted by design
          </div>

          <div class="space-y-5">
            <h1 class="max-w-3xl text-5xl font-black tracking-tight text-white sm:text-6xl lg:text-7xl">
              Run autonomous coding agents on infrastructure you control.
            </h1>
            <p class="max-w-2xl text-lg leading-8 text-slate-300 sm:text-xl">
              Chetter coordinates standard agent harnesses, isolated runners, GitHub-native workflows, and a live web control plane for teams that self-host their automation.
            </p>
          </div>

          <div class="grid max-w-3xl gap-3 sm:grid-cols-3">
            <Card size="xl" class="!p-4 border-white/10 bg-white/10 text-white backdrop-blur">
              <p class="text-sm text-slate-300">Harnesses</p>
              <p class="mt-1 text-xl font-bold">OpenCode, Claude Code, Pi</p>
            </Card>
            <Card size="xl" class="!p-4 border-white/10 bg-white/10 text-white backdrop-blur">
              <p class="text-sm text-slate-300">Runtime</p>
              <p class="mt-1 text-xl font-bold">Docker or Kubernetes</p>
            </Card>
            <Card size="xl" class="!p-4 border-white/10 bg-white/10 text-white backdrop-blur">
              <p class="text-sm text-slate-300">Workflow</p>
              <p class="mt-1 text-xl font-bold">GitHub PRs and issues</p>
            </Card>
          </div>
        </div>

        <Card size="xl" class="!p-0 overflow-hidden border-white/10 bg-white/10 shadow-2xl shadow-cyan-950/40 backdrop-blur">
          <div class="border-b border-white/10 px-4 py-3">
            <div class="flex items-center gap-2">
              <span class="h-3 w-3 rounded-full bg-red-400"></span>
              <span class="h-3 w-3 rounded-full bg-yellow-300"></span>
              <span class="h-3 w-3 rounded-full bg-green-400"></span>
              <span class="ml-3 text-xs font-medium text-slate-300">chetter.example.com</span>
            </div>
          </div>
          <img
            src={`${base}/screenshots/screenshot-2.png`}
            alt="Chetter dashboard showing task status cards and recent tasks"
            class="block w-full rounded-b-lg"
          />
        </Card>
      </section>

      <section class="pb-14 lg:pb-20">
        <div class="mb-8 max-w-3xl">
          <p class="mb-2 text-sm font-semibold uppercase tracking-[0.24em] text-cyan-300">Screenshots</p>
          <h2 class="text-3xl font-black tracking-tight text-white sm:text-4xl">A web UI for operating your own agent fleet.</h2>
          <p class="mt-3 text-slate-300">Observe task runs, manage automation, and inspect GitHub-native workflows from the same self-hosted control plane.</p>
        </div>

        <div class="grid gap-5 lg:grid-cols-3">
          {#each screenshots as shot (shot.src)}
            <Card size="xl" class="!p-0 overflow-hidden border-white/10 bg-white/10 text-white shadow-xl shadow-slate-950/25 backdrop-blur">
              <img src={`${base}${shot.src}`} alt={shot.description} class="aspect-video w-full object-cover object-left-top" />
              <div class="p-5">
                <h3 class="text-lg font-bold">{shot.title}</h3>
                <p class="mt-2 text-sm leading-6 text-slate-300">{shot.description}</p>
              </div>
            </Card>
          {/each}
        </div>

        <div class="mt-10 grid gap-4 md:grid-cols-3">
          <Card size="xl" class="!p-5 border-white/10 bg-white/10 text-white backdrop-blur">
            <p class="text-sm font-semibold uppercase tracking-[0.2em] text-cyan-300">Open Source</p>
            <p class="mt-3 text-2xl font-black">Run it yourself</p>
            <p class="mt-2 text-sm leading-6 text-slate-300">No hosted Chetter service. Clone the repo, deploy the server and runners, and keep the execution path under your control.</p>
          </Card>
          <Card size="xl" class="!p-5 border-white/10 bg-white/10 text-white backdrop-blur">
            <p class="text-sm font-semibold uppercase tracking-[0.2em] text-cyan-300">GitHub Native</p>
            <p class="mt-3 text-2xl font-black">PRs, issues, reviews</p>
            <p class="mt-2 text-sm leading-6 text-slate-300">Wire agents into the tools developers already use, with tracked artifacts and auditable outcomes.</p>
          </Card>
          <Card size="xl" class="!p-5 border-white/10 bg-white/10 text-white backdrop-blur">
            <p class="text-sm font-semibold uppercase tracking-[0.2em] text-cyan-300">Standard Runners</p>
            <p class="mt-3 text-2xl font-black">Plain containers</p>
            <p class="mt-2 text-sm leading-6 text-slate-300">Use Docker or Kubernetes images with the language tools and harnesses your projects already rely on.</p>
          </Card>
        </div>

        <Card size="xl" class="!p-6 mt-10 border-white/10 bg-white/10 text-white backdrop-blur">
          <div class="flex flex-col items-start gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="text-2xl font-black text-white">Want to try Chetter?</h2>
              <p class="mt-1 text-slate-300">Start with the repository, or ask questions in the Discord server.</p>
            </div>
            <div class="flex flex-col gap-2 sm:flex-row" aria-label="Project links">
              {#each communityLinks as link (link.href)}
                <Button href={link.href} target="_blank" rel="noopener noreferrer" color={link.label === "Discord" ? "purple" : "light"} class="justify-center gap-2">
                  <svg class="h-5 w-5" fill="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                    <path d={link.icon} />
                  </svg>
                  {link.action}
                </Button>
              {/each}
            </div>
          </div>
        </Card>
      </section>
    </div>
  </div>
{:else}
  <div class="min-h-screen bg-slate-50 dark:bg-slate-950 flex">

    {#snippet sidebarContainer(isMobile: boolean)}
      {@const widthClass = sidebarCollapsed ? "w-16" : "w-64"}
      <Sidebar activeUrl={activePath} position="static" alwaysOpen={true} activateClickOutside={false} backdrop={false}
               class="{widthClass} {isMobile ? 'fixed inset-y-0 left-0 z-40' : 'h-screen sticky top-0'} bg-white dark:bg-gray-800 border-r border-gray-200 dark:border-gray-700 transition-[width] duration-200">
        {@render sidebarInner()}
      </Sidebar>
    {/snippet}

    <div class="hidden md:block {sidebarCollapsed ? 'w-16' : 'w-64'} flex-shrink-0">
      {@render sidebarContainer(false)}
    </div>

    {#if sidebarOpen}
      <button class="md:hidden fixed inset-0 z-30 bg-gray-900/50 dark:bg-black/60 cursor-default border-0 p-0"
              onclick={() => sidebarOpen = false}
              aria-label="Close sidebar"
              type="button"></button>
      {@render sidebarContainer(true)}
    {/if}

    <div class="flex-1 flex flex-col min-w-0 min-h-screen">
      <div class="md:hidden sticky top-0 z-20 flex items-center gap-3 bg-white dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700 px-4 py-3">
        <SidebarButton onclick={() => sidebarOpen = true} />
        <h1 class="text-lg font-bold text-gray-900 dark:text-white">Chetter</h1>
        <div class="flex-1"></div>
        <button onclick={toggleCollapsed} class="p-1 text-gray-500 dark:text-gray-400" title="Toggle sidebar width">
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            {#if sidebarCollapsed}
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 5l7 7-7 7M5 5l7 7-7 7" />
            {:else}
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 19l-7-7 7-7m8 14l-7-7 7-7" />
            {/if}
          </svg>
        </button>
      </div>

      {#if serverInfo.quotaExhausted}
        <Alert color="red" class="sticky top-0 md:top-0 z-50 rounded-none border-0">
          <div class="flex items-center gap-2">
            <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z"/></svg>
            <span>Database quota exhausted — the TiDB cluster has restricted access. <a href="https://docs.pingcap.com/tidbcloud/serverless-limitations#usage-quota" target="_blank" rel="noopener noreferrer" class="underline font-medium">Increase spending limits</a> to restore full functionality.</span>
          </div>
        </Alert>
      {/if}

      <main class="flex-1 overflow-auto bg-[radial-gradient(circle_at_top_right,rgba(14,165,233,0.10),transparent_30%),linear-gradient(180deg,rgba(248,250,252,0.9),rgba(241,245,249,1))] text-slate-900 dark:bg-[radial-gradient(circle_at_top_right,rgba(56,189,248,0.13),transparent_30%),linear-gradient(180deg,rgba(15,23,42,1),rgba(2,6,23,1))] dark:text-slate-100">
        {@render children()}
      </main>
    </div>

    <Toast />
    <ConfirmDialog />
  </div>
{/if}
