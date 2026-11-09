import { forwardRef } from "react";
import * as Primitive from "@radix-ui/react-checkbox";
import { cx } from "./cx";
import { CheckIcon } from "../components/icons";

export const Checkbox = forwardRef<
  React.ElementRef<typeof Primitive.Root>,
  React.ComponentPropsWithoutRef<typeof Primitive.Root>
>(function Checkbox({ className, ...props }, ref) {
  return (
    <Primitive.Root
      ref={ref}
      className={cx(
        "grid h-4 w-4 shrink-0 place-items-center rounded-sm border border-border-strong bg-surface transition-colors",
        "data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:text-primary-contrast",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-bg",
        "disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    >
      <Primitive.Indicator>
        <CheckIcon size={12} strokeWidth={3} />
      </Primitive.Indicator>
    </Primitive.Root>
  );
});

export default Checkbox;
