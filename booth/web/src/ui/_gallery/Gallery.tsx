import { useState } from "react";
import type { ReactNode } from "react";
import {
  AssignAgent,
  Avatar,
  Badge,
  Button,
  Card,
  CardBody,
  CardFooter,
  CardHeader,
  Checkbox,
  CommentThread,
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogTrigger,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Field,
  Input,
  ReviewPanel,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Separator,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
  Tooltip,
  TooltipProvider,
  ToastProvider,
  useToast,
  CommentMark,
} from "../index";
import type { Assignee, InlineCommentItem, ReviewEntry } from "../index";
import { MoreIcon } from "../../components/icons";

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-4 border-b border-border py-8">
      <h2 className="text-xs font-semibold uppercase tracking-wider text-text-muted">{title}</h2>
      <div className="flex flex-wrap items-start gap-5">{children}</div>
    </section>
  );
}

const assignees: Assignee[] = [
  { id: "gd", name: "Graphic Designer", role: "Illustration agent", isAgent: true },
  { id: "dr", name: "DevRel Writer", role: "Content agent", isAgent: true },
  { id: "amy", name: "Amy Chen", role: "Editor" },
  { id: "sam", name: "Sam Rivera", role: "Reviewer" },
];

const comments: InlineCommentItem[] = [
  {
    id: "1",
    author: { name: "Amy Chen" },
    body: "Can we tighten this opening paragraph? It buries the lede.",
    timeLabel: "2h ago",
  },
  {
    id: "2",
    author: { name: "DevRel Writer", isAgent: true },
    body: "Good call — rewriting to lead with the benchmark number.",
    timeLabel: "1h ago",
  },
];

const reviews: ReviewEntry[] = [
  { id: "r1", reviewer: { name: "Sam Rivera" }, verdict: "approved", note: "Reads great. Ship it.", timeLabel: "3h ago" },
  {
    id: "r2",
    reviewer: { name: "Graphic Designer", isAgent: true },
    verdict: "changes-requested",
    note: "Hero illustration needs the brand gradient, not flat violet.",
    timeLabel: "1h ago",
  },
];

function ToastDemo() {
  const { toast } = useToast();
  return (
    <Button
      variant="secondary"
      onClick={() => toast({ title: "Draft saved", description: "Your changes are in review.", tone: "success" })}
    >
      Fire toast
    </Button>
  );
}

export function Gallery() {
  const [assignee, setAssignee] = useState<string>();

  return (
    <TooltipProvider>
      <ToastProvider>
        <div className="mx-auto max-w-5xl px-8 pb-20">
          <header className="flex items-center justify-between py-8">
            <div>
              <h1 className="text-xl font-semibold text-text">ContentRemixer UI kit</h1>
              <p className="text-sm text-text-muted">
                booth/web/src/ui — Radix primitives + brand tokens (ISI-7210, W7)
              </p>
            </div>
            <GalleryThemeToggle />
          </header>

          <Section title="Buttons">
            <Button variant="primary">Primary</Button>
            <Button variant="secondary">Secondary</Button>
            <Button variant="ghost">Ghost</Button>
            <Button variant="danger">Danger</Button>
            <Button loading>Saving</Button>
            <Button size="sm">Small</Button>
            <Button size="lg">Large</Button>
          </Section>

          <Section title="Badges">
            <Badge tone="neutral" dot>Draft</Badge>
            <Badge tone="primary" dot>In review</Badge>
            <Badge tone="success" dot>Accepted</Badge>
            <Badge tone="warning" dot>Changes</Badge>
            <Badge tone="info" dot>Scraping</Badge>
            <Badge tone="accent">New</Badge>
          </Section>

          <Section title="Form controls">
            <div className="w-64">
              <Field label="Title" hint="Shown in the content list" required>
                {({ id }) => <Input id={id} placeholder="How we cut latency 40%" />}
              </Field>
            </div>
            <div className="w-64">
              <Field label="Summary" error="Summary is required">
                {({ id, invalid }) => <Textarea id={id} invalid={invalid} placeholder="One-line summary…" />}
              </Field>
            </div>
            <div className="flex items-center gap-2">
              <Switch defaultChecked id="g-sw" />
              <label htmlFor="g-sw" className="text-sm text-text">Publish on accept</label>
            </div>
            <div className="flex items-center gap-2">
              <Checkbox defaultChecked id="g-cb" />
              <label htmlFor="g-cb" className="text-sm text-text">Notify reviewers</label>
            </div>
            <div className="w-56">
              <Select defaultValue="blog">
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="blog">Blog post</SelectItem>
                  <SelectItem value="thread">Social thread</SelectItem>
                  <SelectItem value="video">Video script</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </Section>

          <Section title="Overlays">
            <Dialog>
              <DialogTrigger asChild>
                <Button variant="secondary">Open dialog</Button>
              </DialogTrigger>
              <DialogContent title="Discard draft?" description="This cannot be undone.">
                <DialogBody>
                  <p className="text-sm text-text-muted">The draft and its comments will be removed.</p>
                </DialogBody>
                <DialogFooter>
                  <Button variant="ghost">Cancel</Button>
                  <Button variant="danger">Discard</Button>
                </DialogFooter>
              </DialogContent>
            </Dialog>

            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" iconLeft={<MoreIcon size={16} />}>Actions</Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuLabel>Document</DropdownMenuLabel>
                <DropdownMenuItem>Duplicate</DropdownMenuItem>
                <DropdownMenuItem>Export</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem destructive>Delete</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>

            <Tooltip content="Re-run the scraper for this source">
              <Button variant="ghost">Hover me</Button>
            </Tooltip>

            <ToastDemo />
          </Section>

          <Section title="Tabs">
            <Tabs defaultValue="preview" className="w-full">
              <TabsList>
                <TabsTrigger value="preview">Preview</TabsTrigger>
                <TabsTrigger value="comments">Comments</TabsTrigger>
                <TabsTrigger value="review">Review</TabsTrigger>
              </TabsList>
              <TabsContent value="preview">
                <p className="text-sm text-text-muted">Rendered document preview goes here.</p>
              </TabsContent>
              <TabsContent value="comments">
                <p className="text-sm text-text-muted">Inline comments thread list.</p>
              </TabsContent>
              <TabsContent value="review">
                <p className="text-sm text-text-muted">Peer-review verdicts.</p>
              </TabsContent>
            </Tabs>
          </Section>

          <Section title="Avatars & cards">
            <Avatar name="Amy Chen" />
            <Avatar name="Graphic Designer" agent />
            <Avatar name="Sam Rivera" size="lg" />
            <Card className="w-72">
              <CardHeader title="How we cut p99 latency" subtitle="Draft · updated 2h ago" action={<Badge tone="primary" dot>In review</Badge>} />
              <CardBody>
                <p className="text-sm text-text-muted">A short preview of the article body sits here, two lines deep.</p>
              </CardBody>
              <CardFooter>
                <Button variant="ghost" size="sm">Open</Button>
                <Button size="sm">Publish</Button>
              </CardFooter>
            </Card>
          </Section>

          <Section title="Inline comments (Google-Docs style)">
            <p className="max-w-md text-sm leading-7 text-text">
              We benchmarked the new pipeline and found{" "}
              <CommentMark active>a 40% reduction in p99 latency</CommentMark>{" "}
              across the fleet, with{" "}
              <CommentMark resolved>no regression in throughput</CommentMark>.
            </p>
            <CommentThread
              quote="a 40% reduction in p99 latency"
              comments={comments}
              onReply={() => {}}
              onResolve={() => {}}
            />
          </Section>

          <Section title="Peer review + assign an agent">
            <ReviewPanel
              className="w-96"
              reviews={reviews}
              summary="changes-requested"
              headerAction={
                <AssignAgent
                  assignees={assignees}
                  selectedId={assignee}
                  onAssign={(a) => setAssignee(a.id)}
                />
              }
              onSubmitReview={() => {}}
            />
          </Section>

          <Separator />
        </div>
      </ToastProvider>
    </TooltipProvider>
  );
}

function GalleryThemeToggle() {
  const [dark, setDark] = useState(
    typeof document !== "undefined" && document.documentElement.getAttribute("data-theme") === "dark",
  );
  return (
    <Button
      variant="secondary"
      size="sm"
      onClick={() => {
        const next = !dark;
        setDark(next);
        document.documentElement.setAttribute("data-theme", next ? "dark" : "light");
      }}
    >
      {dark ? "Light theme" : "Dark theme"}
    </Button>
  );
}
