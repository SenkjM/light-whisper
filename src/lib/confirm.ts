import { toast } from "sonner";

/** In-app confirm via toast actions (no window.confirm / browser dialogs). */
export function confirmAction(
  message: string,
  opts?: { confirmLabel?: string; cancelLabel?: string; durationMs?: number },
): Promise<boolean> {
  const confirmLabel = opts?.confirmLabel ?? "OK";
  const cancelLabel = opts?.cancelLabel ?? "Cancel";
  const duration = opts?.durationMs ?? 12_000;
  return new Promise((resolve) => {
    let settled = false;
    const finish = (v: boolean) => {
      if (settled) return;
      settled = true;
      toast.dismiss(id);
      resolve(v);
    };
    const id = toast(message, {
      duration,
      action: {
        label: confirmLabel,
        onClick: () => finish(true),
      },
      cancel: {
        label: cancelLabel,
        onClick: () => finish(false),
      },
      onDismiss: () => finish(false),
      onAutoClose: () => finish(false),
    });
  });
}
