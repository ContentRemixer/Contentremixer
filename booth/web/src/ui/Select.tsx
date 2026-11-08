import { forwardRef } from "react";
import type { ReactNode } from "react";
import * as Primitive from "@radix-ui/react-select";
import { cx } from "./cx";
import { CheckIcon, ChevronDownIcon } from "../components/icons";

export const Select = Primitive.Root;
export const SelectValue = Primitive.Value;
export const SelectGroup = Primitive.Group;

export const SelectTrigger = forwardRef<
  React.ElementRef<typeof Primitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof Primitive.Trigger>
>(function SelectTrigger({ className, children, ...props }, ref) {
  return (
    <Primitive.Trigger
      ref={ref}
      className={cx(
        "flex h-10 w-full items-center justify-between gap-2 rounded-md border border-border bg-surface px-3 text-sm text-text",
        "hover:border-border-strong focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary",
        "data-[placeholder]:text-text-muted disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    >
      {children}
      <Primitive.Icon>
        <ChevronDownIcon size={16} className="text-text-muted" />
      </Primitive.Icon>
    </Primitive.Trigger>
  );
});

export const SelectContent = forwardRef<
  React.ElementRef<typeof Primitive.Content>,
  React.ComponentPropsWithoutRef<typeof Primitive.Content>
>(function SelectContent({ className, children, position = "popper", ...props }, ref) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        ref={ref}
        position={position}
        className={cx(
          "z-50 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-md border border-border bg-surface-raised p-1 shadow-card",
          position === "popper" && "mt-1.5",
          className,
        )}
        {...props}
      >
        <Primitive.Viewport>{children}</Primitive.Viewport>
      </Primitive.Content>
    </Primitive.Portal>
  );
});

export const SelectItem = forwardRef<
  React.ElementRef<typeof Primitive.Item>,
  React.ComponentPropsWithoutRef<typeof Primitive.Item> & { children: ReactNode }
>(function SelectItem({ className, children, ...props }, ref) {
  return (
    <Primitive.Item
      ref={ref}
      className={cx(
        "relative flex cursor-pointer select-none items-center rounded-sm py-1.5 pl-7 pr-2.5 text-sm text-text outline-none",
        "data-[highlighted]:bg-surface-2 data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
        className,
      )}
      {...props}
    >
      <span className="absolute left-2 inline-flex items-center">
        <Primitive.ItemIndicator>
          <CheckIcon size={14} />
        </Primitive.ItemIndicator>
      </span>
      <Primitive.ItemText>{children}</Primitive.ItemText>
    </Primitive.Item>
  );
});
