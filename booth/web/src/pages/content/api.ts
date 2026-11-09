// Typed client for the content-collaboration API (plan §3.2 B3). Thin wrappers
// over the shared fetch helper (app/api.ts) — the HMAC session cookie is sent
// automatically; a 401 is surfaced centrally as the session-expired state.

import { api } from "../../app/api";
import type {
  Comment,
  Content,
  CreateCommentRequest,
  CreateIllustrationRequest,
  CreateReviewRequest,
  IllustrationRequest,
  ReviewThread,
  UpdateContentRequest,
  UpdateIllustrationRequest,
} from "./types";

// The server marshals nil slices as JSON null; normalise to [] so callers never
// branch on it.
const arr = <T>(v: T[] | null): T[] => v ?? [];

export const contentApi = {
  list: () => api.get<Content[] | null>("/v1/content").then(arr),

  get: (id: string) => api.get<Content>(`/v1/content/${encodeURIComponent(id)}`),

  update: (id: string, body: UpdateContentRequest) =>
    api.put<Content>(`/v1/content/${encodeURIComponent(id)}`, body),

  listComments: (id: string) =>
    api.get<Comment[] | null>(`/v1/content/${encodeURIComponent(id)}/comments`).then(arr),

  addComment: (id: string, body: CreateCommentRequest) =>
    api.post<Comment>(`/v1/content/${encodeURIComponent(id)}/comments`, body),

  listReviews: (id: string) =>
    api.get<ReviewThread[] | null>(`/v1/content/${encodeURIComponent(id)}/reviews`).then(arr),

  addReview: (id: string, body: CreateReviewRequest) =>
    api.post<ReviewThread>(`/v1/content/${encodeURIComponent(id)}/reviews`, body),

  resolveReview: (id: string, threadId: string) =>
    api.post<ReviewThread>(
      `/v1/content/${encodeURIComponent(id)}/reviews/${encodeURIComponent(threadId)}/resolve`,
    ),

  listIllustrationRequests: (contentId: string) =>
    api
      .get<IllustrationRequest[] | null>("/v1/illustration-requests")
      .then(arr)
      // The collection route has no content filter server-side; narrow here so the
      // detail page shows only this document's requests.
      .then((reqs) => reqs.filter((r) => r.contentId === contentId)),

  requestIllustration: (body: CreateIllustrationRequest) =>
    api.post<IllustrationRequest>("/v1/illustration-requests", body),

  updateIllustration: (id: string, body: UpdateIllustrationRequest) =>
    api.patch<IllustrationRequest>(
      `/v1/illustration-requests/${encodeURIComponent(id)}`,
      body,
    ),
};
