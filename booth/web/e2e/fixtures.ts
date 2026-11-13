import { test as base, expect, type Page, type Route } from "@playwright/test";

// Hermetic Booth API mock for the web E2E suite (ISI-7214, W8).
//
// The SPA talks to Booth over JSON at /v1/* (booth/internal/server). None of that
// backend runs in CI, so every /v1/** request is fulfilled here from an in-memory
// fixture. Tests configure the fixture up front; handlers match on method + path
// and fall through to a 404 so an unmocked call fails loudly instead of hanging.
//
// Tests may register their own page.route("**/v1/...") BEFORE calling installApi
// is unnecessary — Playwright runs the most-recently-added handler first, so call
// installApi first and add per-test overrides after it to take precedence.

export type Role = "Admin" | "DevRel";

export interface Session {
  sub: string;
  role: Role;
}

export interface MockData {
  /** whoami result; null → 401 (anonymous / not signed in). */
  session: Session | null;
  /** bootstrap-status; null → 404 (endpoint absent → normal login). */
  bootstrap: { firstRun: boolean; adminUsername: string } | null;
  /** login outcome: the session to establish, or null → 401 invalid creds. */
  loginAs: Session | null;
  /** onboarding-status (D5); null → default "already onboarded" for the session. */
  onboarding: Record<string, unknown> | null;
  scrapedSources: Record<string, unknown>[];
  content: Record<string, unknown>[];
  contentDetail: Record<
    string,
    {
      doc: Record<string, unknown>;
      comments: Record<string, unknown>[];
      reviews: Record<string, unknown>[];
      illustrations: Record<string, unknown>[];
    }
  >;
  users: Record<string, unknown>[];
  personas: Record<string, unknown>[];
  repos: Record<string, unknown>[];
  models: Record<string, unknown>[];
}

export function defaults(overrides: Partial<MockData> = {}): MockData {
  return {
    session: null,
    bootstrap: null,
    loginAs: null,
    onboarding: null,
    scrapedSources: [],
    content: [],
    contentDetail: {},
    users: [],
    personas: [],
    repos: [],
    models: [],
    ...overrides,
  };
}

function json(route: Route, status: number, body: unknown) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

// installApi wires a single catch-all /v1/** handler against a mutable MockData.
// Returns the live data object so a test can mutate it mid-run (e.g. flip session
// to simulate expiry on the next poll).
export async function installApi(page: Page, data: MockData): Promise<MockData> {
  await page.route("**/v1/**", async (route) => {
    const req = route.request();
    const method = req.method();
    const url = new URL(req.url());
    const p = url.pathname;

    // --- auth ---------------------------------------------------------------
    if (p === "/v1/auth/whoami" && method === "GET") {
      return data.session ? json(route, 200, data.session) : json(route, 401, { error: "unauthorized" });
    }
    if (p === "/v1/auth/bootstrap-status" && method === "GET") {
      return data.bootstrap ? json(route, 200, data.bootstrap) : json(route, 404, { error: "not found" });
    }
    // DevRel first-run probe (ISI-7360 D5). The onboarding gate (D6) probes this
    // for a DevRel session. Default to "already onboarded" so existing specs land
    // on the normal shell; an onboarding-wizard spec can override via its own
    // route or by setting data.onboarding before installApi.
    if (p === "/v1/onboarding-status" && method === "GET") {
      if (!data.session) return json(route, 401, { error: "unauthorized" });
      return json(
        route,
        200,
        data.onboarding ?? {
          subject: data.session.sub,
          role: data.session.role,
          hasPersona: true,
          hasValidatedPersona: true,
          needsOnboarding: false,
        },
      );
    }
    if (p === "/v1/auth/login" && method === "POST") {
      if (!data.loginAs) return json(route, 401, { error: "invalid credentials" });
      data.session = data.loginAs;
      return json(route, 200, data.loginAs);
    }
    if (p === "/v1/auth/logout" && method === "POST") {
      data.session = null;
      return route.fulfill({ status: 204, body: "" });
    }

    // --- scraper review (B6) ------------------------------------------------
    if (p === "/v1/scraped-sources" && method === "GET") {
      const status = url.searchParams.get("status");
      const rows = status
        ? data.scrapedSources.filter((s) => s.status === status)
        : data.scrapedSources;
      return json(route, 200, rows);
    }
    const srcAction = p.match(/^\/v1\/scraped-sources\/([^/]+)\/(accept|rescrape|discard)$/);
    if (srcAction && method === "POST") {
      const [, id, action] = srcAction;
      const status = action === "rescrape" ? "rescraping" : action === "accept" ? "accepted" : "discarded";
      const src = data.scrapedSources.find((s) => s.id === id);
      if (src) src.status = status; // mutate so a follow-up reload reflects the transition
      const next = { ...(src ?? { id }), status };
      if (action === "rescrape") return json(route, 200, { source: next, job: { id: "job-1", status: "queued" } });
      return json(route, 200, next);
    }

    // --- content collaboration (B3) ----------------------------------------
    if (p === "/v1/content" && method === "GET") return json(route, 200, data.content);
    if (p === "/v1/content" && method === "POST") {
      const doc = { id: "new-doc", owner: data.session?.sub ?? "someone", title: "Untitled", bodyMd: "", status: "draft", createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString() };
      return json(route, 201, doc);
    }
    const detail = p.match(/^\/v1\/content\/([^/]+)$/);
    if (detail && method === "GET") {
      const entry = data.contentDetail[detail[1]];
      return entry ? json(route, 200, entry.doc) : json(route, 404, { error: "not found" });
    }
    const comments = p.match(/^\/v1\/content\/([^/]+)\/comments$/);
    if (comments && method === "GET") return json(route, 200, data.contentDetail[comments[1]]?.comments ?? []);
    const reviews = p.match(/^\/v1\/content\/([^/]+)\/reviews$/);
    if (reviews && method === "GET") return json(route, 200, data.contentDetail[reviews[1]]?.reviews ?? []);
    if (p === "/v1/illustration-requests" && method === "GET") {
      const all = Object.values(data.contentDetail).flatMap((d) => d.illustrations);
      return json(route, 200, all);
    }

    // --- admin console (C1-C4) ---------------------------------------------
    if (p === "/v1/users" && method === "GET") return json(route, 200, data.users);
    if (p === "/v1/devrelpersonas" && method === "GET") return json(route, 200, data.personas);
    if (p === "/v1/referencerepos" && method === "GET") return json(route, 200, data.repos);
    if (p === "/v1/modelprofiles" && method === "GET") return json(route, 200, data.models);

    // Anything unmocked fails loudly so a missing stub is obvious in CI.
    return json(route, 404, { error: `unmocked ${method} ${p}` });
  });
  return data;
}

// Fixture-aware test: `page` is a plain Playwright page; use installApi(page, …).
export const test = base;
export { expect };

// --- sample fixtures --------------------------------------------------------

export const ADMIN: Session = { sub: "admin", role: "Admin" };
export const DEVREL: Session = { sub: "robin", role: "DevRel" };

export function source(over: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  const now = new Date(0).toISOString();
  return {
    id: "src-1",
    jobId: "job-1",
    owner: "robin",
    referenceRepo: "acme/docs",
    personaName: "robin",
    sourceUrl: "https://example.com/post",
    sourceKind: "article",
    title: "Scaling Postgres on Kubernetes",
    status: "pending",
    chunkCount: 7,
    createdAt: now,
    updatedAt: now,
    ...over,
  };
}

export function doc(over: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  const now = new Date(0).toISOString();
  return {
    id: "doc-1",
    owner: "robin",
    title: "Kubernetes show notes",
    bodyMd: "# Intro\n\nThis is the draft body for review.",
    status: "draft",
    createdAt: now,
    updatedAt: now,
    ...over,
  };
}
