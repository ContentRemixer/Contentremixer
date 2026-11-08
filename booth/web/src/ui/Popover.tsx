import { forwardRef } from "react";
import * as Primitive from "@radix-ui/react-popover";
import { cx } from "./cx";

export const Popover = Primitive.Root;
export const PopoverTrigger = Primitive.Trigger;
export const PopoverAnchor = Primitive.Anchor;
export const PopoverClose = Primitive.Close;

export const PopoverContent = forwardRef<
  React.ElementRef<typeof Primitive.Content>,
  React.ComponentPropsWithoutRef<typeof Primitive.Content>
>(function PopoverContent({ className, sideOffset = 8, align = "start", ...props }, ref) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        ref={ref}
        sideOffset={sideOffset}
        align={align}
        className={cx(
          "z-50 w-72 rounded-lg border border-border bg-surface-raised p-3 shadow-card focus:outline-none",
          className,
        )}
        {...props}
      />
    </Primitive.Portal>
  );
});
