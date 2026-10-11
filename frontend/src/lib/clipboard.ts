export async function copyText(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value);
      return;
    } catch {
      // HTTP origins and clipboard permissions may require the selection-based fallback.
    }
  }

  const active = document.activeElement;
  const input = document.createElement("textarea");
  input.value = value;
  input.readOnly = true;
  Object.assign(input.style, { position: "fixed", left: "0", top: "0", width: "1px", height: "1px", opacity: "0", fontSize: "16px" });
  // Keep focus inside a modal's focus trap when copying from the creation dialog.
  (active?.closest('[role="dialog"]') ?? document.body).appendChild(input);
  try {
    input.focus({ preventScroll: true });
    input.select();
    input.setSelectionRange(0, value.length);
    if (!document.execCommand("copy")) throw new Error("Clipboard is unavailable");
  } finally {
    input.remove();
    if (active instanceof HTMLElement && active.isConnected) active.focus({ preventScroll: true });
  }
}
