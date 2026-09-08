import type { ReactNode } from "react";
import * as RadixDropdownMenu from "radix-ui/dropdown-menu";
import Check from "lucide-react/dist/esm/icons/check.mjs";
import { Button, TooltipText } from "./primitives";
export function IconMenu({ label, icon, value, items, onValueChange }: {
  label: string;
  icon: ReactNode;
  value: string;
  items: Array<{
    value: string;
    label: string;
    icon?: ReactNode;
  }>;
  onValueChange: (value: string) => void;
}) {
  return <RadixDropdownMenu.Root>
    <TooltipText label={label}>
      <RadixDropdownMenu.Trigger asChild>
        <Button aria-label={label} title={label} size="sm" variant="ghost" className="w-7 px-0">
          {icon}</Button>
      </RadixDropdownMenu.Trigger>
    </TooltipText>
    <RadixDropdownMenu.Portal>
      <RadixDropdownMenu.Content align="end" sideOffset={6} className="z-50 min-w-36 rounded-lg border border-app-border bg-app-surface p-1 shadow-xl">
        <RadixDropdownMenu.RadioGroup value={value} onValueChange={onValueChange}>
          {items.map((item) => <RadixDropdownMenu.RadioItem key={item.value} value={item.value} className="relative flex h-8 cursor-default select-none items-center gap-2 rounded-md pl-7 pr-3 text-sm outline-none data-[highlighted]:bg-app-hover">
            <RadixDropdownMenu.ItemIndicator className="absolute left-2">
              <Check aria-hidden="true" className="size-3" strokeWidth={2.25} />
            </RadixDropdownMenu.ItemIndicator>
            {item.icon}<span>
              {item.label}</span>
          </RadixDropdownMenu.RadioItem>)}</RadixDropdownMenu.RadioGroup>
      </RadixDropdownMenu.Content>
    </RadixDropdownMenu.Portal>
  </RadixDropdownMenu.Root>;
}
