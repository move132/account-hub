import * as PopoverPrimitive from "@radix-ui/react-popover";
import { forwardRef, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import CalendarDays from "lucide-react/dist/esm/icons/calendar-days.mjs";
import ChevronLeft from "lucide-react/dist/esm/icons/chevron-left.mjs";
import ChevronRight from "lucide-react/dist/esm/icons/chevron-right.mjs";
import Clock3 from "lucide-react/dist/esm/icons/clock-3.mjs";
import X from "lucide-react/dist/esm/icons/x.mjs";
import { Button, cn, Select } from "./primitives";

const weekdays = ["一", "二", "三", "四", "五", "六", "日"];
const twoDigitValues = Array.from({ length: 60 }, (_, value) => String(value).padStart(2, "0"));
const hourValues = twoDigitValues.slice(0, 24);

type DateControlProps = {
  id?: string;
  value: string;
  onValueChange: (value: string) => void;
  min?: string;
  max?: string;
  placeholder?: string;
  className?: string;
  disabled?: boolean;
  required?: boolean;
  "aria-label"?: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
};

function parseDate(value: string) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return null;
  const [, year, month, day] = match.map(Number);
  const parsed = new Date(year, month - 1, day);
  return parsed.getFullYear() === year && parsed.getMonth() === month - 1 && parsed.getDate() === day ? parsed : null;
}

function formatDate(value: Date) {
  return [value.getFullYear(), String(value.getMonth() + 1).padStart(2, "0"), String(value.getDate()).padStart(2, "0")].join("-");
}

function displayDate(value: string) {
  const parsed = parseDate(value);
  return parsed ? `${parsed.getFullYear()}年${parsed.getMonth() + 1}月${parsed.getDate()}日` : value;
}

function dateInRange(value: string, min?: string, max?: string) {
  return (!min || value >= min) && (!max || value <= max);
}

function initialMonth(selected: string, min?: string, max?: string) {
  const selectedDate = parseDate(selected);
  if (selectedDate) return new Date(selectedDate.getFullYear(), selectedDate.getMonth(), 1);
  const today = formatDate(new Date());
  if (min && today < min) {
    const minimum = parseDate(min);
    if (minimum) return new Date(minimum.getFullYear(), minimum.getMonth(), 1);
  }
  if (max && today > max) {
    const maximum = parseDate(max);
    if (maximum) return new Date(maximum.getFullYear(), maximum.getMonth(), 1);
  }
  const now = new Date();
  return new Date(now.getFullYear(), now.getMonth(), 1);
}

function CalendarPanel({ selected, min, max, onSelect, footer }: { selected: string; min?: string; max?: string; onSelect: (value: string) => void; footer?: ReactNode }) {
  const [month, setMonth] = useState(() => initialMonth(selected, min, max));
  const dayButtons = useRef(new Map<string, HTMLButtonElement>());
  const pendingFocus = useRef<string | null>(null);
  const year = month.getFullYear();
  const monthIndex = month.getMonth();
  const firstWeekday = (new Date(year, monthIndex, 1).getDay() + 6) % 7;
  const daysInMonth = new Date(year, monthIndex + 1, 0).getDate();
  const today = formatDate(new Date());
  const previousMonthLastDay = formatDate(new Date(year, monthIndex, 0));
  const nextMonthFirstDay = formatDate(new Date(year, monthIndex + 1, 1));
  const previousDisabled = Boolean(min && previousMonthLastDay < min);
  const nextDisabled = Boolean(max && nextMonthFirstDay > max);

  useLayoutEffect(() => {
    if (pendingFocus.current) {
      dayButtons.current.get(pendingFocus.current)?.focus();
      pendingFocus.current = null;
    }
  }, [month]);

  const moveDayFocus = (event: KeyboardEvent<HTMLButtonElement>, value: string) => {
    const current = parseDate(value);
    if (!current) return;
    const next = new Date(current);
    const weekday = (current.getDay() + 6) % 7;
    switch (event.key) {
      case "ArrowLeft": next.setDate(next.getDate() - 1); break;
      case "ArrowRight": next.setDate(next.getDate() + 1); break;
      case "ArrowUp": next.setDate(next.getDate() - 7); break;
      case "ArrowDown": next.setDate(next.getDate() + 7); break;
      case "Home": next.setDate(next.getDate() - weekday); break;
      case "End": next.setDate(next.getDate() + 6 - weekday); break;
      case "PageUp":
      case "PageDown": {
        const offset = (event.key === "PageUp" ? -1 : 1) * (event.shiftKey ? 12 : 1);
        next.setDate(1);
        next.setMonth(next.getMonth() + offset);
        next.setDate(Math.min(current.getDate(), new Date(next.getFullYear(), next.getMonth() + 1, 0).getDate()));
        break;
      }
      default: return;
    }
    event.preventDefault();
    const nextValue = formatDate(next);
    if (!dateInRange(nextValue, min, max)) return;
    pendingFocus.current = nextValue;
    setMonth(new Date(next.getFullYear(), next.getMonth(), 1));
  };

  return (
    <div className="w-[min(18.25rem,calc(100vw-1.5rem))] p-3 sm:w-[268px] sm:p-2.5">
      <div className="mb-2 flex items-center justify-between">
        <button type="button" className="grid size-8 place-items-center rounded-md text-app-subtle hover:bg-app-hover hover:text-app-foreground disabled:opacity-30 sm:size-7" disabled={previousDisabled} onClick={() => setMonth(new Date(year, monthIndex - 1, 1))} aria-label="上个月">
          <ChevronLeft aria-hidden="true" size={14} />
        </button>
        <strong aria-live="polite" className="text-xs font-medium text-app-foreground">{year} 年 {monthIndex + 1} 月</strong>
        <button type="button" className="grid size-8 place-items-center rounded-md text-app-subtle hover:bg-app-hover hover:text-app-foreground disabled:opacity-30 sm:size-7" disabled={nextDisabled} onClick={() => setMonth(new Date(year, monthIndex + 1, 1))} aria-label="下个月">
          <ChevronRight aria-hidden="true" size={14} />
        </button>
      </div>

      <div className="grid grid-cols-7 text-center text-[11px] text-app-subtle">
        {weekdays.map((weekday) => <span className="py-1.5" key={weekday}>{weekday}</span>)}
      </div>
      <div className="grid grid-cols-7 gap-0.5">
        {Array.from({ length: firstWeekday }, (_, index) => <span key={`blank-${index}`} aria-hidden="true" />)}
        {Array.from({ length: daysInMonth }, (_, index) => {
          const day = index + 1;
          const dateValue = formatDate(new Date(year, monthIndex, day));
          const isSelected = dateValue === selected;
          const isToday = dateValue === today;
          return (
            <button
              type="button"
              key={dateValue}
              ref={(element) => { if (element) dayButtons.current.set(dateValue, element); else dayButtons.current.delete(dateValue); }}
              data-calendar-day
              disabled={!dateInRange(dateValue, min, max)}
              aria-label={`${year}年${monthIndex + 1}月${day}日`}
              aria-pressed={isSelected}
              aria-current={isToday ? "date" : undefined}
              onClick={() => onSelect(dateValue)}
              onKeyDown={(event) => moveDayFocus(event, dateValue)}
              className={cn(
                "grid h-9 w-full min-w-0 place-items-center rounded-md text-xs outline-none focus-visible:ring-1 focus-visible:ring-app-primary disabled:pointer-events-none disabled:opacity-25 sm:h-8",
                isToday && !isSelected && "border border-app-primary/50",
                isSelected ? "bg-app-primary font-semibold text-white hover:bg-app-primary-hover" : "hover:bg-app-hover focus-visible:bg-app-hover",
                !isSelected && (isToday ? "text-app-primary" : "text-app-foreground"),
              )}
            >
              {day}
            </button>
          );
        })}
      </div>
      {footer}
    </div>
  );
}

const DateTrigger = forwardRef<HTMLButtonElement, Omit<DateControlProps, "onValueChange" | "min" | "max" | "required"> & { icon: ReactNode; "aria-required"?: boolean }>(function DateTrigger({ value, placeholder, icon, className, ...props }, ref) {
  return (
    <button
      ref={ref}
      type="button"
      className={cn(
        "flex h-8 w-full min-w-0 items-center gap-2 rounded-md border border-app-border bg-app-muted px-2 text-left text-xs transition-colors hover:bg-app-hover focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50 data-[state=open]:border-app-primary",
        value ? "text-app-foreground" : "text-app-subtle",
        className,
      )}
      {...props}
    >
      {icon}
      <span className="min-w-0 flex-1 truncate">{value || placeholder}</span>
    </button>
  );
});

function focusCalendar(event: Event) {
  if (!(event.target instanceof HTMLElement)) return;
  const target = event.target.querySelector<HTMLButtonElement>('[data-calendar-day][aria-pressed="true"]:not(:disabled)')
    ?? event.target.querySelector<HTMLButtonElement>('[data-calendar-day][aria-current="date"]:not(:disabled)')
    ?? event.target.querySelector<HTMLButtonElement>('[data-calendar-day]:not(:disabled)');
  if (target) {
    event.preventDefault();
    target.focus({ preventScroll: true });
  }
}

export const DatePicker = forwardRef<HTMLButtonElement, DateControlProps>(function DatePicker(
  { value, onValueChange, min, max, placeholder = "选择日期", required, ...triggerProps },
  ref,
) {
  const [open, setOpen] = useState(false);
  return (
    <PopoverPrimitive.Root open={open} onOpenChange={setOpen}>
      <PopoverPrimitive.Trigger asChild>
        <DateTrigger ref={ref} value={value ? displayDate(value) : ""} placeholder={placeholder} aria-required={required} icon={<CalendarDays aria-hidden="true" className="shrink-0 text-app-subtle" size={14} />} {...triggerProps} />
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content aria-label={triggerProps["aria-label"] ?? "选择日期"} onOpenAutoFocus={focusCalendar} align="start" sideOffset={4} collisionPadding={10} className="z-50 max-h-[var(--radix-popover-content-available-height)] overflow-y-auto rounded-lg border border-app-border bg-app-surface shadow-xl outline-none">
          <CalendarPanel
            selected={value}
            min={min}
            max={max}
            onSelect={(nextValue) => {
              onValueChange(nextValue);
              setOpen(false);
            }}
            footer={value && !required ? <div className="mt-2 border-t border-app-border pt-2"><button type="button" className="flex h-8 items-center gap-1.5 rounded-md px-2 text-xs text-app-subtle hover:bg-app-hover hover:text-app-foreground sm:h-7" onClick={() => { onValueChange(""); setOpen(false); }}><X aria-hidden="true" size={12} />清除日期</button></div> : undefined}
          />
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  );
});

DatePicker.displayName = "DatePicker";

function TimeColumn({ label, unit, values, value, onValueChange }: {
  label: string;
  unit: "hour" | "minute";
  values: string[];
  value: string;
  onValueChange: (value: string) => void;
}) {
  const viewport = useRef<HTMLDivElement>(null);
  const selected = useRef<HTMLButtonElement>(null);

  useLayoutEffect(() => {
    if (viewport.current && selected.current) {
      viewport.current.scrollTop = selected.current.offsetTop - (viewport.current.clientHeight - selected.current.offsetHeight) / 2;
    }
  }, [value]);

  const moveSelection = (event: KeyboardEvent<HTMLButtonElement>, part: string) => {
    const index = values.indexOf(part);
    let next = index;
    switch (event.key) {
      case "ArrowUp": next = Math.max(0, index - 1); break;
      case "ArrowDown": next = Math.min(values.length - 1, index + 1); break;
      case "Home": next = 0; break;
      case "End": next = values.length - 1; break;
      case "PageUp": next = Math.max(0, index - 5); break;
      case "PageDown": next = Math.min(values.length - 1, index + 5); break;
      default: return;
    }
    event.preventDefault();
    onValueChange(values[next]);
    event.currentTarget.parentElement?.querySelector<HTMLButtonElement>(`[data-time-value="${values[next]}"]`)?.focus({ preventScroll: true });
  };

  return <div className="min-w-0">
    <p className="mb-1.5 text-center text-xs text-app-subtle">{label}</p>
    <div ref={viewport} role="listbox" aria-label={label} aria-orientation="vertical" data-time-unit={unit}
      className="relative flex h-44 flex-col gap-0.5 overflow-y-auto overscroll-contain rounded-md border border-app-border bg-app-muted p-1">
      {values.map((part) => <button key={part} ref={part === value ? selected : undefined} type="button" role="option"
        aria-label={`${part}${unit === "hour" ? "时" : "分"}`} aria-selected={part === value} tabIndex={part === value ? 0 : -1}
        data-time-value={part} onClick={() => onValueChange(part)} onKeyDown={(event) => moveSelection(event, part)}
        className={cn("h-8 w-full shrink-0 rounded text-sm tabular-nums outline-none focus-visible:ring-1 focus-visible:ring-app-primary",
          part === value ? "bg-app-primary font-semibold text-white dark:text-black" : "text-app-foreground hover:bg-app-hover")}>
        {part}
      </button>)}
    </div>
  </div>;
}

function TimePanel({ value, required, onCommit, onClear }: {
  value: string;
  required?: boolean;
  onCommit: (value: string) => void;
  onClear: () => void;
}) {
  const parsed = /^([01]\d|2[0-3]):([0-5]\d)$/.exec(value);
  const now = new Date();
  const [hour, setHour] = useState(parsed?.[1] ?? String(now.getHours()).padStart(2, "0"));
  const [minute, setMinute] = useState(parsed?.[2] ?? String(now.getMinutes()).padStart(2, "0"));
  const commit = () => onCommit(`${hour}:${minute}`);

  return <div className="w-52 max-w-[calc(100vw-1.5rem)] p-3" onKeyDown={(event) => {
    if (event.key === "Enter" && event.target instanceof HTMLElement && event.target.getAttribute("role") === "option") {
      event.preventDefault();
      commit();
    }
  }}>
    <div className="grid grid-cols-2 gap-2">
      <TimeColumn label="小时" unit="hour" values={hourValues} value={hour} onValueChange={setHour} />
      <TimeColumn label="分钟" unit="minute" values={twoDigitValues} value={minute} onValueChange={setMinute} />
    </div>
    <div className="mt-2 flex items-center justify-between gap-2 border-t border-app-border pt-2">
      {value && !required ? <Button type="button" size="sm" variant="ghost" onClick={onClear} className="text-app-subtle">
        <X aria-hidden="true" className="size-3" />清除时间
      </Button> : <span />}
      <Button type="button" size="sm" variant="primary" onClick={commit}>确定</Button>
    </div>
  </div>;
}

function focusTime(event: Event) {
  if (!(event.target instanceof HTMLElement)) return;
  const target = event.target.querySelector<HTMLButtonElement>('[data-time-unit="hour"] [aria-selected="true"]');
  if (target) {
    event.preventDefault();
    target.focus({ preventScroll: true });
  }
}

export const TimePicker = forwardRef<HTMLButtonElement, Omit<DateControlProps, "min" | "max">>(function TimePicker(
  { value, onValueChange, placeholder = "选择时间", required, ...triggerProps },
  ref,
) {
  const [open, setOpen] = useState(false);
  return <PopoverPrimitive.Root open={open} onOpenChange={setOpen}>
    <PopoverPrimitive.Trigger asChild>
      <DateTrigger ref={ref} value={value} placeholder={placeholder} aria-required={required}
        icon={<Clock3 aria-hidden="true" className="shrink-0 text-app-subtle" size={14} />} {...triggerProps} />
    </PopoverPrimitive.Trigger>
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content aria-label={triggerProps["aria-label"] ?? "选择时间"} onOpenAutoFocus={focusTime}
        align="start" sideOffset={4} collisionPadding={10}
        className="z-50 max-h-[var(--radix-popover-content-available-height)] overflow-y-auto rounded-lg border border-app-border bg-app-surface shadow-xl outline-none">
        <TimePanel value={value} required={required}
          onCommit={(nextValue) => { onValueChange(nextValue); setOpen(false); }}
          onClear={() => { onValueChange(""); setOpen(false); }} />
      </PopoverPrimitive.Content>
    </PopoverPrimitive.Portal>
  </PopoverPrimitive.Root>;
});

TimePicker.displayName = "TimePicker";

function parseDateTime(value: string) {
  const match = /^(\d{4}-\d{2}-\d{2}) (\d{2}):(\d{2}):(\d{2})$/.exec(value);
  if (!match || !parseDate(match[1])) return null;
  const hour = Number(match[2]);
  const minute = Number(match[3]);
  const second = Number(match[4]);
  if (hour > 23 || minute > 59 || second > 59) return null;
  return { date: match[1], hour: match[2], minute: match[3], second: match[4] };
}

function DateTimePanel({ value, min, max, required, onCommit, onClear }: { value: string; min?: string; max?: string; required?: boolean; onCommit: (value: string) => void; onClear: () => void }) {
  const parsed = parseDateTime(value);
  const now = new Date();
  const [date, setDate] = useState(parsed?.date || (dateInRange(formatDate(now), min, max) ? formatDate(now) : min || max || formatDate(now)));
  const [hour, setHour] = useState(parsed?.hour || String(now.getHours()).padStart(2, "0"));
  const [minute, setMinute] = useState(parsed?.minute || String(now.getMinutes()).padStart(2, "0"));
  const [second, setSecond] = useState(parsed?.second || "00");

  return (
    <CalendarPanel
      selected={date}
      min={min}
      max={max}
      onSelect={setDate}
      footer={(
        <div className="mt-2 border-t border-app-border pt-2">
          <div className="mb-2 flex items-center gap-1.5 text-xs text-app-subtle"><Clock3 aria-hidden="true" size={13} />时间</div>
          <div className="grid grid-cols-3 gap-2 [&>button]:min-w-0 [&>button]:w-full">
            <Select value={hour} onValueChange={setHour} ariaLabel="小时" options={hourValues.map((part) => ({ value: part, label: `${part} 时` }))} />
            <Select value={minute} onValueChange={setMinute} ariaLabel="分钟" options={twoDigitValues.map((part) => ({ value: part, label: `${part} 分` }))} />
            <Select value={second} onValueChange={setSecond} ariaLabel="秒" options={twoDigitValues.map((part) => ({ value: part, label: `${part} 秒` }))} />
          </div>
          <div className="mt-2 flex items-center justify-between gap-2">
            {!required && value ? <button type="button" className="flex h-8 items-center gap-1.5 rounded-md px-2 text-xs text-app-subtle hover:bg-app-hover hover:text-app-foreground sm:h-7" onClick={onClear}><X aria-hidden="true" size={12} />清除</button> : <span />}
            <button type="button" className="h-8 rounded-md bg-app-primary px-3 text-xs font-semibold text-white hover:bg-app-primary-hover sm:h-7" onClick={() => onCommit(`${date} ${hour}:${minute}:${second}`)}>确定</button>
          </div>
        </div>
      )}
    />
  );
}

export const DateTimePicker = forwardRef<HTMLButtonElement, DateControlProps>(function DateTimePicker(
  { value, onValueChange, min, max, placeholder = "选择日期和时间", required, ...triggerProps },
  ref,
) {
  const [open, setOpen] = useState(false);
  return (
    <PopoverPrimitive.Root open={open} onOpenChange={setOpen}>
      <PopoverPrimitive.Trigger asChild>
        <DateTrigger ref={ref} value={value} placeholder={placeholder} aria-required={required} icon={<Clock3 aria-hidden="true" className="shrink-0 text-app-subtle" size={14} />} {...triggerProps} />
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content aria-label={triggerProps["aria-label"] ?? "选择日期和时间"} onOpenAutoFocus={focusCalendar} align="start" sideOffset={4} collisionPadding={10} className="z-50 max-h-[var(--radix-popover-content-available-height)] overflow-y-auto rounded-lg border border-app-border bg-app-surface shadow-xl outline-none">
          <DateTimePanel
            value={value}
            min={min}
            max={max}
            required={required}
            onCommit={(nextValue) => { onValueChange(nextValue); setOpen(false); }}
            onClear={() => { onValueChange(""); setOpen(false); }}
          />
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  );
});

DateTimePicker.displayName = "DateTimePicker";
