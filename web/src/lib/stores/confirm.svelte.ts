export interface ConfirmState {
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  resolve: (value: boolean) => void;
}

let current = $state<ConfirmState | null>(null);

export function getConfirm() {
  return current;
}

export function confirm(opts: {
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
}): Promise<boolean> {
  return new Promise((resolve) => {
    current = {
      title: opts.title,
      message: opts.message,
      confirmLabel: opts.confirmLabel,
      cancelLabel: opts.cancelLabel,
      resolve,
    };
  });
}

export function resolveConfirm(value: boolean) {
  if (current) {
    current.resolve(value);
    current = null;
  }
}
