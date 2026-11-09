import { forwardRef } from "react";
import * as Primitive from "@radix-ui/react-tabs";
import { cx } from "./cx";

export const Tabs = Primitive.Root;

export const TabsList = forwardRef<
  React.ElementRef<typeof Primitive.List>,
  React.ComponentPropsWithoutRef<typeof Primitive.List>
>(function TabsList({ className, ...props }, ref) {
  return (
    <Primitive.List
      ref={ref}
      className={cx("flex items-center gap-1 border-b border-border", className)}
      {...props}
    />
  );
});

export const TabsTrigger = forwardRef<
  React.ElementRef<typeof Primitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof Primitive.Trigger>
>(function TabsTrigger({ className, ...props }, ref) {
  return (
    <Primitive.Trigger
      ref={ref}
      className={cx(
        "-mb-px border-b-2 border-transparent px-3 py-2 text-sm font-medium text-text-muted transition-colors",
        "hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary rounded-t-sm",
        "data-[state=active]:border-primary data-[state=active]:text-text",
        className,
      )}
      {...props}
    />
  );
});

export const TabsContent = forwardRef<
  React.ElementRef<typeof Primitive.Content>,
  React.ComponentPropsWithoutRef<typeof Primitive.Content>
>(function TabsContent({ className, ...props }, ref) {
  return (
    <Primitive.Content
      ref={ref}
      className={cx("pt-4 focus-visible:outline-none", className)}
      {...props}
    />
  );
});
