<script lang="ts">
  import { Toast, ToastContainer } from "flowbite-svelte";
  import { getToasts } from "$lib/stores/toast.svelte";

  let toasts = $derived(getToasts());
</script>

{#if toasts.length > 0}
  <ToastContainer position="bottom-right">
    {#each toasts as toast (toast.id)}
      <Toast color={toast.kind === "success" ? "green" : toast.kind === "error" ? "red" : "primary"} dismissable={false}>
        {toast.message}
      </Toast>
    {/each}
  </ToastContainer>
{/if}
