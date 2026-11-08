import { forwardRef } from "react";
import * as Primitive from "@radix-ui/react-dropdown-menu";
import { cx } from "./cx";
import { CheckIcon } from "../components/icons";

// Keyboard-navigable menu (roving focus, typeahead) on Radix DropdownMenu.
export const DropdownMenu = Primitive.Root;
export const DropdownMenuTrigger = Primitive.Trigger;
export const DropdownMenuGroup = Primitive.Group;

export const DropdownMenuContent = forwardRef<
  React.ElementRef<typeof Primitive.Content>,
  React.ComponentPropsWithoutRef<typeof Primitive.Content>
>(function DropdownMenuContent({ className, sideOffset = 6, ...props }, ref) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        ref={ref}
        sideOffset={sideOffset}
        className={cx(
          "z-50 min-w-[11rem] overflow-hidden rounded-md border border-border bg-surface-raised p-1 shadow-card",
          className,
        )}
        {...props}
      />
    </Primitive.Portal>
  );
});

const itemBase =
  "relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2.5 py-1.5 text-sm text-text outline-none " +
  "data-[highlighted]:bg-surface-2 data-[disabled]:pointer-events-none data-[disabled]:opacity-50";

export const DropdownMenuItem = forwardRef<
  React.ElementRef<typeof Primitive.Item>,
  React.ComponentPropsWithoutRef<typeof Primitive.Item> & { destructive?: boolean }
>(function DropdownMenuItem({ className, destructive, ...props }, ref) {
  return (
    <Primitive.Item
      ref={ref}
      className={cx(itemBase, destructive && "text-warn data-[highlighted]:bg-warn/10", className)}
      {...props}
    />
  );
});

export const DropdownMenuCheckboxItem = forwardRef<
  React.ElementRef<typeof Primitive.CheckboxItem>,
  React.ComponentPropsWithoutRef<typeof Primitive.CheckboxItem>
>(function DropdownMenuCheckboxItem({ className, children, ...props }, ref) {
  return (
    <Primitive.CheckboxItem ref={ref} className={cx(itemBase, "pl-7", className)} {...props}>
      <Primitive.ItemIndicator className="absolute left-2">
        <CheckIcon size={14} />
      </Primitive.ItemIndicator>
      {children}
    </Primitive.CheckboxItem>
  );
});

export function DropdownMenuLabel({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <Primitive.Label
      className={cx("px-2.5 py-1.5 text-xs font-semibold uppercase tracking-wide text-text-muted", className)}
      {...props}
    />
  );
}

export function DropdownMenuSeparator({ className }: { className?: string }) {
  return <Primitive.Separator className={cx("my-1 h-px bg-border", className)} />;
}
