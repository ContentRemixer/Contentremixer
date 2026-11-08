import { forwardRef } from "react";
import type { ReactNode } from "react";
import * as Primitive from "@radix-ui/react-tooltip";
import { cx } from "./cx";

// Wrap the app root once in <TooltipProvider> (re-exported) then use <Tooltip>.
export const TooltipProvider = Primitive.Provider;

export const Tooltip = forwardRef<
  React.ElementRef<typeof Primitive.Content>,
  {
    content: ReactNode;
    children: ReactNode;
    side?: Primitive.TooltipContentProps["side"];
    className?: string;
  }
>(function Tooltip({ content, children, side = "top", className }, ref) {
  return (
    <Primitive.Root>
      <Primitive.Trigger asChild>{children}</Primitive.Trigger>
      <Primitive.Portal>
        <Primitive.Content
          ref={ref}
          side={side}
          sideOffset={6}
          className={cx(
            "z-50 max-w-xs rounded-md bg-text px-2.5 py-1.5 text-xs font-medium text-bg shadow-card",
            className,
          )}
        >
          {content}
          <Primitive.Arrow className="fill-text" />
        </Primitive.Content>
      </Primitive.Portal>
    </Primitive.Root>
  );
});
