import { useEffect, useState } from "react";
import { api, ApiError } from "./api";

// DevRel self-service first-run detection (ISI-7360 D5, plan ISI-7350 §1/§4).
// Distinct from the seed-Admin bootstrap probe (bootstrap.ts): that one asks
// "is this a freshly provisioned install?"; this one asks, for the already
// logged-in DevRel, "do I have a validated persona yet?" — and if not, the SPA
// launches the onboarding wizard (D6, ISI-7361) instead of the normal shell.
//
// Contract (served by the Booth API — booth/internal/server/onboarding.go):
//   GET /v1/onboarding-status → 200 {
//     subject, role, hasPersona, hasValidatedPersona, needsOnboarding
//   }
// The probe runs behind the session Middleware, so it is only meaningful once
// authenticated. On any non-200 (endpoint absent on an older Booth, network,
// parse) it degrades to needsOnboarding:false so the standard shell renders
// unchanged — a probe failure must never trap a user in onboarding.
export interface OnboardingStatus {
  subject: string;
  role: string;
  hasPersona: boolean;
  hasValidatedPersona: boolean;
  needsOnboarding: boolean;
}

const ONBOARDING_PATH = "/v1/onboarding-status";

const NOT_ONBOARDING: OnboardingStatus = {
  subject: "",
  role: "",
  hasPersona: false,
  hasValidatedPersona: false,
  needsOnboarding: false,
};

export type OnboardingProbe =
  | { state: "loading" }
  | { state: "ready"; status: OnboardingStatus };

// useOnboardingStatus probes once while enabled (the caller enables it only for
// an authenticated session). D6's wizard gate consumes `status.needsOnboarding`.
export function useOnboardingStatus(enabled: boolean): OnboardingProbe {
  const [probe, setProbe] = useState<OnboardingProbe>(
    enabled ? { state: "loading" } : { state: "ready", status: NOT_ONBOARDING },
  );

  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    (async () => {
      try {
        const s = await api.get<OnboardingStatus>(ONBOARDING_PATH);
        if (alive) {
          setProbe({
            state: "ready",
            status: {
              subject: s.subject ?? "",
              role: s.role ?? "",
              hasPersona: !!s.hasPersona,
              hasValidatedPersona: !!s.hasValidatedPersona,
              needsOnboarding: !!s.needsOnboarding,
            },
          });
        }
      } catch (err) {
        // Endpoint absent/unreachable → treat as not-onboarding so the standard
        // shell renders. A 401 here is handled centrally by the AuthProvider
        // (session gone); we still degrade locally so this hook never traps.
        void (err instanceof ApiError);
        if (alive) setProbe({ state: "ready", status: NOT_ONBOARDING });
      }
    })();
    return () => {
      alive = false;
    };
  }, [enabled]);

  return probe;
}
