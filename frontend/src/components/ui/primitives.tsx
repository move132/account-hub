import { forwardRef, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type TextareaHTMLAttributes } from "react";
import * as Slot from "radix-ui/slot";
import * as Label from "radix-ui/label";
import * as RadixSwitch from "radix-ui/switch";
import * as RadixSelect from "radix-ui/select";
import * as RadixDialog from "radix-ui/dialog";
import * as AlertDialog from "radix-ui/alert-dialog";
import * as Tooltip from "radix-ui/tooltip";
import * as RadixCheckbox from "radix-ui/checkbox";
import * as RadixProgress from "radix-ui/progress";
import Check from "lucide-react/dist/esm/icons/check.mjs";
import ChevronDown from "lucide-react/dist/esm/icons/chevron-down.mjs";
import X from "lucide-react/dist/esm/icons/x.mjs";
const CheckIcon = () => <Check aria-hidden="true" className="size-3" strokeWidth={2.25} />;
const ChevronDownIcon = () => <ChevronDown aria-hidden="true" className="size-3.5" />;
const CloseIcon = () => <X aria-hidden="true" className="size-3.5" />;
export function cn(...values: Array<string | false | null | undefined>) { return values.filter(Boolean).join(" "); }
type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "danger" | "ghost";
  size?: "sm" | "md";
  asChild?: boolean;
};
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant = "secondary", size = "md", asChild, ...props }, ref) => {
  const Component = asChild ? Slot.Root : "button";
  return <Component ref={ref} className={cn("inline-flex items-center justify-center gap-1.5 rounded-md border font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-app-ring disabled:pointer-events-none disabled:opacity-45", size === "sm" ? "h-7 px-2.5 text-xs" : "h-9 px-3.5 text-sm", variant === "primary" && "border-app-primary bg-app-primary text-white hover:bg-app-primary/90", variant === "secondary" && "border-app-border bg-app-surface hover:bg-app-hover", variant === "danger" && "border-app-danger/35 bg-app-danger/10 text-app-danger hover:bg-app-danger/20", variant === "ghost" && "border-transparent bg-transparent hover:bg-app-hover", className)} {...props} />;
});
Button.displayName = "Button";
export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(({ className, ...props }, ref) => <input ref={ref} className={cn("h-8 w-full rounded-md border border-app-border bg-app-muted px-2 text-xs text-app-foreground outline-none placeholder:text-app-subtle focus:border-app-primary focus:ring-2 focus:ring-app-ring/20 disabled:opacity-50", className)} {...props} />);
Input.displayName = "Input";
export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(({ className, ...props }, ref) => <textarea ref={ref} className={cn("min-h-16 w-full resize-y rounded-md border border-app-border bg-app-muted px-2 py-1.5 text-xs text-app-foreground outline-none placeholder:text-app-subtle focus:border-app-primary focus:ring-2 focus:ring-app-ring/20", className)} {...props} />);
Textarea.displayName = "Textarea";
export const FileInput = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(({ className, ...props }, ref) => <input ref={ref} type="file" className={cn("block w-full rounded-md border border-app-border bg-app-muted p-1 text-xs text-app-subtle file:mr-2 file:rounded file:border-0 file:bg-app-hover file:px-2 file:py-0.5 file:text-app-foreground", className)} {...props} />);
FileInput.displayName = "FileInput";
export function Field({ label, children, hint }: {
  label: string;
  children: ReactNode;
  hint?: string;
}) {
  return <Label.Root className="grid gap-1.5 text-sm font-medium">
    <span>
      {label}</span>
    {children}{hint && <span className="text-xs font-normal text-app-subtle">
      {hint}</span>}</Label.Root>;
}
export function Switch({ checked, onCheckedChange, disabled }: {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  disabled?: boolean;
}) {
  return <RadixSwitch.Root checked={checked} onCheckedChange={onCheckedChange} disabled={disabled} className="relative h-5 w-9 rounded-full border border-app-border bg-app-muted outline-none transition-colors data-[state=checked]:border-app-primary data-[state=checked]:bg-app-primary focus-visible:ring-2 focus-visible:ring-app-ring disabled:opacity-50">
    <RadixSwitch.Thumb className="block size-4 translate-x-0.5 rounded-full bg-white shadow transition-transform data-[state=checked]:translate-x-[1rem]" />
  </RadixSwitch.Root>;
}
export function Checkbox({ checked, onCheckedChange, label, disabled }: {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  label: string;
  disabled?: boolean;
}) {
  return <RadixCheckbox.Root aria-label={label} checked={checked} disabled={disabled} onCheckedChange={(value) => onCheckedChange(value === true)} className="grid size-4 place-items-center rounded border border-app-border bg-app-muted text-[11px] text-black outline-none data-[state=checked]:border-app-primary data-[state=checked]:bg-app-primary focus-visible:ring-2 focus-visible:ring-app-ring disabled:pointer-events-none disabled:opacity-40">
    <RadixCheckbox.Indicator>
      <CheckIcon />
    </RadixCheckbox.Indicator>
  </RadixCheckbox.Root>;
}
export function Progress({ value, label = "进度" }: {
  value: number;
  label?: string;
}) {
  const normalized = Math.max(0, Math.min(100, value));
  return <RadixProgress.Root value={normalized} aria-label={label} className="h-1.5 overflow-hidden rounded-full bg-app-muted">
    <RadixProgress.Indicator className="h-full rounded-full bg-app-primary transition-transform" style={{ transform: `translateX(-${100 - normalized}%)` }} />
  </RadixProgress.Root>;
}
export function Select({ value, onValueChange, options, placeholder, ariaLabel = "选择选项" }: {
  value?: string;
  onValueChange: (value: string) => void;
  options: Array<{
    value: string;
    label: string;
  }>;
  placeholder?: string;
  ariaLabel?: string;
}) {
  return <RadixSelect.Root value={value} onValueChange={onValueChange}>
    <RadixSelect.Trigger aria-label={ariaLabel} className="inline-flex h-8 min-w-28 items-center justify-between gap-2 rounded-md border border-app-border bg-app-muted px-2 text-xs outline-none focus:ring-2 focus:ring-app-ring">
      <RadixSelect.Value placeholder={placeholder} />
      <RadixSelect.Icon>
        <ChevronDownIcon />
      </RadixSelect.Icon>
    </RadixSelect.Trigger>
    <RadixSelect.Portal>
      <RadixSelect.Content position="popper" sideOffset={6} className="z-50 overflow-hidden rounded-lg border border-app-border bg-app-surface p-1 shadow-xl">
        <RadixSelect.Viewport>
          {options.map((option) => <RadixSelect.Item key={option.value} value={option.value} className="relative flex h-7 min-w-32 cursor-default select-none items-center rounded-md pl-7 pr-2.5 text-xs outline-none data-[highlighted]:bg-app-hover">
            <RadixSelect.ItemIndicator className="absolute left-2">
              <CheckIcon />
            </RadixSelect.ItemIndicator>
            <RadixSelect.ItemText>
              {option.label}</RadixSelect.ItemText>
          </RadixSelect.Item>)}</RadixSelect.Viewport>
      </RadixSelect.Content>
    </RadixSelect.Portal>
  </RadixSelect.Root>;
}
export function Dialog({ open, onOpenChange, title, description, children, contentClassName }: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children: ReactNode;
  contentClassName?: string;
}) {
  return <RadixDialog.Root open={open} onOpenChange={onOpenChange}>
    <RadixDialog.Portal>
      <RadixDialog.Overlay className="fixed inset-0 z-40 bg-black/70 backdrop-blur-[2px]" />
      <RadixDialog.Content className={cn("fixed left-1/2 top-1/2 z-50 max-h-[88vh] w-[min(92vw,680px)] -translate-x-1/2 -translate-y-1/2 overflow-auto overscroll-contain rounded-xl border border-app-border bg-app-surface p-5 shadow-2xl outline-none", contentClassName)}>
        <div className="mb-4 pr-7">
          <RadixDialog.Title className="text-balance text-base font-semibold">
            {title}</RadixDialog.Title>
          {description && <RadixDialog.Description className="mt-1 break-all text-sm text-app-subtle">
            {description}</RadixDialog.Description>}</div>
        {children}<RadixDialog.Close className="absolute right-3 top-3 grid size-7 place-items-center rounded-md text-app-subtle outline-none hover:bg-app-hover hover:text-app-foreground focus-visible:ring-2 focus-visible:ring-app-ring" aria-label="关闭">
          <CloseIcon />
        </RadixDialog.Close>
      </RadixDialog.Content>
    </RadixDialog.Portal>
  </RadixDialog.Root>;
}
export function ConfirmDialog({ open, onOpenChange, title, description, confirmLabel = "确认", onConfirm, danger }: {
  open: boolean;
  onOpenChange: (value: boolean) => void;
  title: string;
  description: string;
  confirmLabel?: string;
  onConfirm: () => void;
  danger?: boolean;
}) {
  return <AlertDialog.Root open={open} onOpenChange={onOpenChange}>
    <AlertDialog.Portal>
      <AlertDialog.Overlay className="fixed inset-0 z-40 bg-black/70" />
      <AlertDialog.Content className="fixed left-1/2 top-1/2 z-50 w-[min(92vw,420px)] -translate-x-1/2 -translate-y-1/2 overscroll-contain rounded-xl border border-app-border bg-app-surface p-5 shadow-2xl">
        <AlertDialog.Title className="text-balance text-base font-semibold">
          {title}</AlertDialog.Title>
        <AlertDialog.Description className="mt-1.5 text-sm leading-5 text-app-subtle">
          {description}</AlertDialog.Description>
        <div className="mt-5 flex justify-end gap-2">
          <AlertDialog.Cancel asChild>
            <Button>取消</Button>
          </AlertDialog.Cancel>
          <AlertDialog.Action asChild>
            <Button variant={danger ? "danger" : "primary"} onClick={onConfirm}>
              {confirmLabel}</Button>
          </AlertDialog.Action>
        </div>
      </AlertDialog.Content>
    </AlertDialog.Portal>
  </AlertDialog.Root>;
}
export function TooltipText({ label, children }: {
  label: string;
  children: ReactNode;
}) {
  return <Tooltip.Provider delayDuration={300}>
    <Tooltip.Root>
      <Tooltip.Trigger asChild>
        {children}</Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content sideOffset={6} className="z-50 rounded-md border border-app-border bg-app-surface px-2 py-1 text-xs shadow-lg">
          {label}<Tooltip.Arrow className="fill-app-surface" />
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  </Tooltip.Provider>;
}
