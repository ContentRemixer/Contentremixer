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
  // --- DevRel onboarding + persona management (D6/D7, ISI-7361/7362) -------
  /** synthesized style profiles keyed by persona name; GET → 404 when absent (D3). */
  styleProfiles: Record<string, Record<string, unknown>>;
  /** POST /v1/content-test response (D4); null → a default sample. */
  contentTest: Record<string, unknown> | null;
  /** POST /v1/promote response (D9); null → 404 (route unmounted on this Booth). */
  promote: Record<string, unknown> | null;
  /** status a freshly-created scrape job reports (D2). "succeeded" → O6 never polls. */
  scrapeJobCreateStatus: string;
  /** status GET /v1/scrape-jobs/{id} reports when the wizard polls a running job. */
  scrapeJobPollStatus: string;
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
    styleProfiles: {},
    contentTest: null,
    promote: null,
    scrapeJobCreateStatus: "succeeded",
    scrapeJobPollStatus: "succeeded",
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
  let jobSeq = 0; // monotonic scrape-job id so O6 progress rows stay distinct
  const epoch = new Date(0).toISOString();
  await page.route("**/v1/**", async (route) => {
    const req = route.request();
    const method = req.method();
    const url = new URL(req.url());
    const p = url.pathname;
    const bodyOf = (): Record<string, unknown> => {
      try {
        return (req.postDataJSON?.() as Record<string, unknown>) ?? {};
      } catch {
        return {};
      }
    };

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

    // --- DevRel persona CRUD (D5/D7) ---------------------------------------
    // POST creates the caller's persona; the server stamps owner from the
    // session (owner is omitted by the client), so we mirror that here.
    if (p === "/v1/devrelpersonas" && method === "POST") {
      const b = bodyOf();
      const spec = { ...((b.spec as Record<string, unknown>) ?? {}), owner: { subject: data.session?.sub ?? "robin" } };
      const created = { metadata: (b.metadata as Record<string, unknown>) ?? { name: "robin" }, spec, status: {} };
      data.personas.push(created);
      return json(route, 201, created);
    }
    const personaByName = p.match(/^\/v1\/devrelpersonas\/([^/]+)$/);
    if (personaByName) {
      const name = decodeURIComponent(personaByName[1]);
      const idx = data.personas.findIndex((x) => (x.metadata as { name?: string })?.name === name);
      if (method === "GET") {
        return idx >= 0 ? json(route, 200, data.personas[idx]) : json(route, 404, { error: "not found" });
      }
      if (method === "PUT") {
        const b = bodyOf();
        const next = { ...(idx >= 0 ? data.personas[idx] : {}), metadata: b.metadata, spec: b.spec, status: (idx >= 0 ? data.personas[idx].status : {}) ?? {} };
        if (idx >= 0) data.personas[idx] = next;
        else data.personas.push(next);
        return json(route, 200, next);
      }
    }

    // --- style profile: read (D7) / synthesize (D3/D6) / validate (D4) -----
    const synth = p.match(/^\/v1\/devrelpersonas\/([^/]+)\/style-profile\/synthesize$/);
    if (synth && method === "POST") {
      const name = decodeURIComponent(synth[1]);
      const sp = { ...(data.styleProfiles[name] ?? styleProfile({ personaName: name })), validated: false, synthesizedAt: epoch };
      data.styleProfiles[name] = sp;
      return json(route, 200, sp);
    }
    const spRead = p.match(/^\/v1\/devrelpersonas\/([^/]+)\/style-profile$/);
    if (spRead && method === "GET") {
      const sp = data.styleProfiles[decodeURIComponent(spRead[1])];
      return sp ? json(route, 200, sp) : json(route, 404, { error: "no profile yet" });
    }
    const validate = p.match(/^\/v1\/devrelpersonas\/([^/]+)\/validate$/);
    if (validate && method === "POST") {
      const name = decodeURIComponent(validate[1]);
      const sp = { ...(data.styleProfiles[name] ?? styleProfile({ personaName: name })), validated: true, validatedAt: epoch };
      data.styleProfiles[name] = sp;
      const per = data.personas.find((x) => (x.metadata as { name?: string })?.name === name);
      if (per) {
        const prev = ((per.status as { conditions?: Record<string, unknown>[] })?.conditions ?? []).filter(
          (c) => c.type !== "Validated",
        );
        per.status = { ...((per.status as Record<string, unknown>) ?? {}), conditions: [...prev, { type: "Validated", status: "True" }] };
      }
      return json(route, 200, sp);
    }

    // --- content test (D4) + promote (D9) ----------------------------------
    if (p === "/v1/content-test" && method === "POST") {
      const b = bodyOf();
      return json(route, 200, data.contentTest ?? contentTestResult({ personaName: (b.personaName as string) ?? "robin" }));
    }
    if (p === "/v1/promote" && method === "POST") {
      return data.promote ? json(route, 200, data.promote) : json(route, 404, { error: "promote route not mounted" });
    }

    // --- scrape jobs (D2) ---------------------------------------------------
    if (p === "/v1/scrape-jobs" && method === "POST") {
      const b = bodyOf();
      const job = {
        id: `job-${++jobSeq}`,
        owner: data.session?.sub ?? "robin",
        referenceRepo: "",
        personaName: (b.personaName as string) ?? "robin",
        sourceUrl: (b.sourceUrl as string) ?? "",
        sourceKind: (b.sourceKind as string) ?? "article",
        status: data.scrapeJobCreateStatus,
        createdAt: epoch,
        updatedAt: epoch,
      };
      return json(route, 201, job);
    }
    const scrapeJob = p.match(/^\/v1\/scrape-jobs\/([^/]+)$/);
    if (scrapeJob && method === "GET") {
      return json(route, 200, {
        id: decodeURIComponent(scrapeJob[1]),
        owner: data.session?.sub ?? "robin",
        referenceRepo: "",
        personaName: "robin",
        sourceUrl: "",
        sourceKind: "article",
        status: data.scrapeJobPollStatus,
        createdAt: epoch,
        updatedAt: epoch,
      });
    }

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

// --- DevRel onboarding + persona fixtures (D6/D7) ---------------------------

// persona builds a DevRelPersona CR for the P1–P4 surfaces. Defaults land the
// "robin" persona at Ready + validated (a returning member); override status to
// model a mid-ingestion or unvalidated persona.
export function persona(over: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  return {
    metadata: { name: "robin" },
    spec: {
      owner: { subject: "robin" },
      selfDescription: "Warm and concrete, for working developers. Short sentences, real examples.",
      primaryContentKind: "blog",
      references: [{ url: "https://example.com/posts/scaling", kind: "article" }],
      contentFormatTargets: [],
    },
    status: {
      phase: "Ready",
      exemplarCount: 7,
      conditions: [
        { type: "Ready", status: "True" },
        { type: "Validated", status: "True" },
      ],
    },
    ...over,
  };
}

// styleProfile builds a review.StyleProfile (traits + sample) the O7 / P1
// surfaces render verbatim. Unvalidated by default (the wizard validates it).
export function styleProfile(over: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  const now = new Date(0).toISOString();
  return {
    personaName: "robin",
    owner: "robin",
    traits: "Direct and concrete. Example-first, short sentences, no hype.",
    sample: "Idempotent deploys mean you can ship the same release twice and sleep at night.",
    validated: false,
    sourceCount: 3,
    synthesizedAt: now,
    ...over,
  };
}

// contentTestResult builds a contenttest.TestResult (the O8 / P4 sample).
export function contentTestResult(over: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  return {
    personaName: "robin",
    sample: "Here is a short, concrete take in your voice on the topic you picked.",
    modelUsed: "test-model",
    validated: false,
    ...over,
  };
}

// promoteResult builds a promote.Result (the P4 suggested LinkedIn post, D9).
export function promoteResult(over: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  return {
    personaName: "robin",
    hook: "Most deploys fail for boring reasons.",
    body: "The boring reason is usually state. Make the release idempotent and the fear goes away.",
    cta: "What's your rollback story?",
    post: "Most deploys fail for boring reasons.\n\nThe boring reason is usually state.\n\nWhat's your rollback story?",
    modelUsed: "test-model",
    ...over,
  };
}

// onboardingStatus builds the D5 first-run probe payload. needsOnboarding:true
// routes a DevRel into the wizard; override to model a set-up member.
export function onboardingStatus(over: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  return {
    subject: "robin",
    role: "DevRel",
    hasPersona: false,
    hasValidatedPersona: false,
    needsOnboarding: true,
    ...over,
  };
}
