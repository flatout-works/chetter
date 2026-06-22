<script lang="ts">
  import { Modal, Button } from "flowbite-svelte";
  import { getConfirm, resolveConfirm } from "$lib/stores/confirm.svelte";
  import type { ConfirmState } from "$lib/stores/confirm.svelte";

  let dialog: ConfirmState | null = $state(null);
  let open = $state(false);

  $effect(() => {
    dialog = getConfirm();
  });

  $effect(() => {
    open = dialog !== null;
  });
</script>

<Modal title={dialog?.title ?? ""} bind:open={open} size="sm" autoclose onclose={() => resolveConfirm(false)}>
  <p class="text-base leading-relaxed text-gray-500 dark:text-gray-400 mb-4">{dialog?.message ?? ""}</p>
  <div class="flex justify-end gap-2">
    <Button color="alternative" onclick={() => resolveConfirm(false)}>
      {dialog?.cancelLabel ?? "Cancel"}
    </Button>
    <Button color="red" onclick={() => resolveConfirm(true)}>
      {dialog?.confirmLabel ?? "Confirm"}
    </Button>
  </div>
</Modal>
