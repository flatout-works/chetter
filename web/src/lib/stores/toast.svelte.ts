export interface Toast {
  id: number;
  message: string;
  kind: "success" | "error" | "info";
}

let nextId = 0;
const toasts = $state<Toast[]>([]);

export function getToasts() {
  return toasts;
}

export function addToast(message: string, kind: "success" | "error" | "info" = "info") {
  const id = nextId++;
  toasts.push({ id, message, kind });
  setTimeout(() => {
    const idx = toasts.findIndex((t) => t.id === id);
    if (idx >= 0) toasts.splice(idx, 1);
  }, 4000);
}
