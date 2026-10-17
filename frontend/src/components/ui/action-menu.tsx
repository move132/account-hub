import { Fragment, type ReactNode } from "react";
import * as RadixDropdownMenu from "radix-ui/dropdown-menu";
import Ellipsis from "lucide-react/dist/esm/icons/ellipsis.mjs";
import { IconButton, cn } from "./primitives";
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
      <IconButton aria-label={label} title={label} className="data-[state=open]:bg-app-hover data-[state=open]:text-app-foreground">
        <Ellipsis aria-hidden="true" />
      </IconButton>
    </RadixDropdownMenu.Trigger>
    <RadixDropdownMenu.Portal>
      <RadixDropdownMenu.Content align="end" side={side} sideOffset={5} collisionPadding={8} className="z-50 min-w-32 max-w-[calc(100vw-1rem)] rounded-lg border border-app-border/80 bg-app-surface p-1 shadow-lg shadow-black/8 outline-none dark:shadow-black/30">
        {items.map((item, index) => <Fragment key={item.label}>
          {item.danger && index > 0 && !items[index - 1].danger && <RadixDropdownMenu.Separator className="mx-1 my-1 h-px bg-app-border/70" />}
          <RadixDropdownMenu.Item disabled={item.disabled} onSelect={() => void item.onSelect()} className={cn(
            "flex h-7 cursor-pointer select-none items-center gap-2 whitespace-nowrap rounded px-2.5 text-[12px] leading-none outline-none transition-colors data-[disabled]:pointer-events-none data-[disabled]:opacity-40 [&_svg]:size-[13px] [&_svg]:shrink-0 [&_svg]:[stroke-width:1.75]",
            item.danger
              ? "text-app-danger data-[highlighted]:bg-app-danger/8"
              : "text-app-foreground [&_svg]:text-app-subtle data-[highlighted]:bg-app-hover data-[highlighted]:[&_svg]:text-app-foreground",
          )}>
            {item.icon}<span>{item.label}</span>
          </RadixDropdownMenu.Item>
        </Fragment>)}
      </RadixDropdownMenu.Content>
    </RadixDropdownMenu.Portal>
  </RadixDropdownMenu.Root>;
}
