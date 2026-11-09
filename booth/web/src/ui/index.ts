// ContentRemixer in-house component library (ISI-7210, W7).
//
// Headless Radix primitives + brand design tokens (branding/kit/tokens.json →
// src/styles/tokens.css/.ts). Every component is theme-aware through the
// semantic CSS vars — no hex is hand-copied here. Downstream UI issues
// (W3 scraper, W5 content, W6 admin) compose exclusively from this module.

export { cx } from "./cx";
export type { ClassValue } from "./cx";

export { Button } from "./Button";
export type { ButtonProps, ButtonVariant, ButtonSize } from "./Button";

export { Field, Label, Input, Textarea } from "./Field";
export type { FieldProps, InputProps, TextareaProps } from "./Field";

export { Badge } from "./Badge";
export type { BadgeProps, BadgeTone } from "./Badge";

export { Card, CardHeader, CardBody, CardFooter } from "./Card";
export type { CardProps } from "./Card";

export {
  Dialog,
  DialogTrigger,
  DialogClose,
  DialogContent,
  DialogBody,
  DialogFooter,
} from "./Dialog";

export {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuGroup,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuCheckboxItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "./DropdownMenu";

export { Tooltip, TooltipProvider } from "./Tooltip";

export { Tabs, TabsList, TabsTrigger, TabsContent } from "./Tabs";

export { Avatar } from "./Avatar";
export type { AvatarProps } from "./Avatar";

export { Switch } from "./Switch";
export { Checkbox } from "./Checkbox";

export {
  Select,
  SelectValue,
  SelectGroup,
  SelectTrigger,
  SelectContent,
  SelectItem,
} from "./Select";

export {
  Popover,
  PopoverTrigger,
  PopoverAnchor,
  PopoverClose,
  PopoverContent,
} from "./Popover";

export { Separator } from "./Separator";

export { ToastProvider, useToast } from "./Toast";
export type { ToastOptions, ToastTone } from "./Toast";

// Collaboration components (B3 inline comments, B4 peer review + assign-agent)
export {
  CommentMark,
  CommentThread,
  CommentComposer,
} from "./InlineComment";
export type {
  InlineCommentItem,
  InlineCommentAuthor,
  CommentThreadProps,
  CommentComposerProps,
} from "./InlineComment";

export { ReviewPanel, ReviewStatusBadge } from "./ReviewPanel";
export type { ReviewPanelProps, ReviewEntry, ReviewVerdict } from "./ReviewPanel";

export { AssignAgent } from "./AssignAgent";
export type { AssignAgentProps, Assignee } from "./AssignAgent";
