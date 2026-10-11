import * as SelectPrimitive from "radix-ui/select";
import { Children, Fragment, forwardRef, isValidElement, type ChangeEvent, type OptionHTMLAttributes, type ReactNode } from "react";
import { Check, ChevronDown, ChevronUp } from "lucide-react";
import { cn } from "./primitives";

const EMPTY_VALUE = "__workbench_empty_select_value__";

type OptionDescriptor = {
  value: string;
  label: ReactNode;
  disabled: boolean;
};

export type SelectProps = {
  id?: string;
  name?: string;
  value?: string;
  defaultValue?: string;
  onChange?: (event: ChangeEvent<HTMLSelectElement>) => void;
  onValueChange?: (value: string) => void;
  children: ReactNode;
  className?: string;
  placeholder?: string;
  disabled?: boolean;
  required?: boolean;
  autoFocus?: boolean;
  tabIndex?: number;
  "aria-label"?: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
};

function optionValue(value: string) {
  return value === "" ? EMPTY_VALUE : value;
}

function collectOptions(children: ReactNode, result: OptionDescriptor[] = []) {
  Children.forEach(children, (child) => {
    if (!isValidElement<OptionHTMLAttributes<HTMLOptionElement>>(child)) return;
    if (child.type === Fragment) {
      collectOptions(child.props.children, result);
      return;
    }
    if (child.type !== "option") return;

    const label = child.props.label ?? child.props.children;
    const fallbackValue = typeof label === "string" || typeof label === "number" ? String(label) : "";
    result.push({
      value: String(child.props.value ?? fallbackValue),
      label,
      disabled: Boolean(child.props.disabled),
    });
  });
  return result;
}

export const Select = forwardRef<HTMLButtonElement, SelectProps>(function Select(
  {
    id,
    name,
    value,
    defaultValue,
    onChange,
    onValueChange,
    children,
    className,
    placeholder = "请选择",
    disabled,
    required,
    autoFocus,
    tabIndex,
    "aria-label": ariaLabel,
    "aria-labelledby": ariaLabelledBy,
    "aria-describedby": ariaDescribedBy,
    "aria-invalid": ariaInvalid,
  },
  ref,
) {
  const options = collectOptions(children);
  const handleValueChange = (nextInternalValue: string) => {
    const nextValue = nextInternalValue === EMPTY_VALUE ? "" : nextInternalValue;
    onValueChange?.(nextValue);
    if (onChange) {
      const target = { value: nextValue } as HTMLSelectElement;
      onChange({ target, currentTarget: target } as ChangeEvent<HTMLSelectElement>);
    }
  };

  return (
    <SelectPrimitive.Root
      name={name}
      value={value === undefined ? undefined : optionValue(value)}
      defaultValue={defaultValue === undefined ? undefined : optionValue(defaultValue)}
      onValueChange={handleValueChange}
      disabled={disabled}
      required={required}
    >
      <SelectPrimitive.Trigger
        ref={ref}
        id={id}
        autoFocus={autoFocus}
        tabIndex={tabIndex}
        aria-label={ariaLabel}
        aria-labelledby={ariaLabelledBy}
        aria-describedby={ariaDescribedBy}
        aria-invalid={ariaInvalid}
        className={cn(
          "flex h-9 min-w-28 items-center justify-between gap-1.5 rounded-md border border-[var(--line-soft)] bg-[var(--surface)] px-2.5 text-left text-xs text-[var(--text-primary)] transition-colors hover:border-[var(--line-strong)] focus-visible:border-[var(--accent)] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50 data-[placeholder]:text-[var(--text-muted)] data-[state=open]:border-[var(--accent)] sm:h-8",
          className,
        )}
      >
        <SelectPrimitive.Value placeholder={placeholder} />
        <SelectPrimitive.Icon asChild>
          <ChevronDown className="shrink-0 text-[var(--text-muted)]" size={14} />
        </SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>

      <SelectPrimitive.Portal>
        <SelectPrimitive.Content
          position="popper"
          sideOffset={3}
          collisionPadding={10}
          className="z-[120] max-h-[min(300px,var(--radix-select-content-available-height))] min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-md border border-[var(--line-strong)] bg-[var(--surface)] p-1 shadow-2xl shadow-black/60"
        >
          <SelectPrimitive.ScrollUpButton className="flex h-7 items-center justify-center text-[var(--text-muted)] sm:h-6">
            <ChevronUp size={13} />
          </SelectPrimitive.ScrollUpButton>
          <SelectPrimitive.Viewport>
            {options.map((option, index) => (
              <SelectPrimitive.Item
                key={`${option.value}-${index}`}
                value={optionValue(option.value)}
                disabled={option.disabled}
                className="relative flex min-h-9 cursor-default select-none items-center rounded px-2 py-1 pl-7 text-xs text-[var(--text-secondary)] outline-none data-[disabled]:pointer-events-none data-[disabled]:opacity-40 data-[highlighted]:bg-[var(--surface-selected)] data-[highlighted]:text-[var(--text-primary)] data-[state=checked]:text-[var(--accent-text)] sm:min-h-7"
              >
                <SelectPrimitive.ItemIndicator className="absolute left-2 inline-flex items-center">
                  <Check size={12} />
                </SelectPrimitive.ItemIndicator>
                <SelectPrimitive.ItemText>{option.label}</SelectPrimitive.ItemText>
              </SelectPrimitive.Item>
            ))}
          </SelectPrimitive.Viewport>
          <SelectPrimitive.ScrollDownButton className="flex h-7 items-center justify-center text-[var(--text-muted)] sm:h-6">
            <ChevronDown size={13} />
          </SelectPrimitive.ScrollDownButton>
        </SelectPrimitive.Content>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  );
});

Select.displayName = "Select";
