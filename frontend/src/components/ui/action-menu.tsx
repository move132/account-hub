import type { ReactNode } from "react";
import * as RadixDropdownMenu from "radix-ui/dropdown-menu";
import Ellipsis from "lucide-react/dist/esm/icons/ellipsis.mjs";
import { Button, cn } from "./primitives";
type ActionMenuItem = {
  label: string;
  icon?: ReactNode;
  onSelect: () => void | Promise<void>;
  danger?: boolean;
  disabled?: boolean;
};
export function ActionMenu({ label, items, side = "bottom" }: {
  label: string;
  items: ActionMenuItem[];
  side?: "top" | "right" | "bottom" | "left";
}) {
  return <RadixDropdownMenu.Root modal={false}>
    <RadixDropdownMenu.Trigger asChild>
      <Button aria-label={label} title={label} size="sm" variant="ghost" className="size-8 px-0">
        <Ellipsis aria-hidden="true" className="size-4.5" />
      </Button>
    </RadixDropdownMenu.Trigger>
    <RadixDropdownMenu.Portal>
      <RadixDropdownMenu.Content align="end" side={side} sideOffset={6} className="z-50 min-w-36 rounded-lg border border-app-border bg-app-surface p-1 shadow-xl">
        {items.map((item) => <RadixDropdownMenu.Item key={item.label} disabled={item.disabled} onSelect={() => void item.onSelect()} className={cn("flex h-8 cursor-default select-none items-center gap-2 rounded-md px-3 text-sm outline-none data-[highlighted]:bg-app-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-45", item.danger && "text-app-danger data-[highlighted]:bg-app-danger/10")}>
          {item.icon}<span>
            {item.label}</span>
        </RadixDropdownMenu.Item>)}
      </RadixDropdownMenu.Content>
    </RadixDropdownMenu.Portal>
  </RadixDropdownMenu.Root>;
}
