import { forwardRef } from "react";
import * as Primitive from "@radix-ui/react-switch";
import { cx } from "./cx";

export const Switch = forwardRef<
  React.ElementRef<typeof Primitive.Root>,
  React.ComponentPropsWithoutRef<typeof Primitive.Root>
>(function Switch({ className, ...props }, ref) {
  return (
    <Primitive.Root
      ref={ref}
      className={cx(
        "peer inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-pill border border-transparent transition-colors",
        "bg-border-strong data-[state=checked]:bg-primary",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-bg",
        "disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    >
      <Primitive.Thumb className="pointer-events-none block h-4 w-4 translate-x-0.5 rounded-pill bg-white shadow transition-transform data-[state=checked]:translate-x-[18px]" />
    </Primitive.Root>
  );
});

export default Switch;
