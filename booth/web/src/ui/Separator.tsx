import * as Primitive from "@radix-ui/react-separator";
import { cx } from "./cx";

export function Separator({
  className,
  orientation = "horizontal",
  ...props
}: React.ComponentPropsWithoutRef<typeof Primitive.Root>) {
  return (
    <Primitive.Root
      orientation={orientation}
      className={cx(
        "bg-border",
        orientation === "horizontal" ? "h-px w-full" : "h-full w-px",
        className,
      )}
      {...props}
    />
  );
}

export default Separator;
