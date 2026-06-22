<script lang="ts">
  import { Modal, Button } from "flowbite-svelte";
  import { getConfirm, resolveConfirm } from "$lib/stores/confirm.svelte";

  let state = $derived(getConfirm());
  let open = $derived(state !== null);
</script>

<Modal title={state?.title ?? ""} bind:open={open} size="sm" autoclose onclose={() => resolveConfirm(false)}>
  <p class="text-base leading-relaxed text-gray-500 dark:text-gray-400 mb-4">{state?.message ?? ""}</p>
  <div class="flex justify-end gap-2">
    <Button color="alternative" onclick={() => resolveConfirm(false)}>
      {state?.cancelLabel ?? "Cancel"}
    </Button>
    <Button color="red" onclick={() => resolveConfirm(true)}>
      {state?.confirmLabel ?? "Confirm"}
    </Button>
  </div>
</Modal>
