<script lang="ts">
  import { onMount } from "svelte";
  import { settings, updateSettings, TIMEZONES } from "$lib/stores/settings.svelte";
  import { get } from "svelte/store";
  import { Card, Select, Label, Button } from "flowbite-svelte";
  import { addToast } from "$lib/stores/toast.svelte";

  let themeVal: string = $state("system");
  let timeFormat: string = $state("24h");
  let timezone: string = $state("");

  onMount(() => {
    const s = get(settings);
    themeVal = s.theme;
    timeFormat = s.timeFormat;
    timezone = s.timezone;
  });

  function handleSave() {
    updateSettings({
      theme: themeVal as "light" | "dark" | "system",
      timeFormat: timeFormat as "12h" | "24h",
      timezone,
    });
    addToast("Settings saved", "success");
  }
</script>

<svelte:head><title>Settings — Chetter</title></svelte:head>

<div class="p-6">
  <div class="flex flex-wrap items-center justify-between mb-6 gap-3">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Settings</h1>
  </div>

  <div class="max-w-xl space-y-6">
    <Card size="xl" class="!p-5">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white mb-4">Appearance</h2>
      <div class="flex flex-col gap-1.5">
        <Label for="settings-theme">Theme</Label>
        <Select id="settings-theme" bind:value={themeVal}>
          <option value="system">System</option>
          <option value="light">Light</option>
          <option value="dark">Dark</option>
        </Select>
      </div>
    </Card>

    <Card size="xl" class="!p-5">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white mb-4">Date &amp; Time</h2>
      <div class="space-y-4">
        <div class="flex flex-col gap-1.5">
          <Label for="settings-timeformat">Time format</Label>
          <Select id="settings-timeformat" bind:value={timeFormat}>
            <option value="24h">24-hour</option>
            <option value="12h">12-hour</option>
          </Select>
        </div>

        <div class="flex flex-col gap-1.5">
          <Label for="settings-timezone">Timezone</Label>
          <Select id="settings-timezone" bind:value={timezone}>
            <option value="">Browser default</option>
            {#each TIMEZONES as tz (tz.tz)}
              <option value={tz.tz}>{tz.city}</option>
            {/each}
          </Select>
        </div>
      </div>
    </Card>

    <Button color="blue" onclick={handleSave}>Save Settings</Button>
  </div>
</div>
