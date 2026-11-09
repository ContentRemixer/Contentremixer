import { useEffect, useState } from "react";
import { api, ApiError } from "./api";

// First-run Admin bootstrap (A1). The seed Admin is provisioned into Booth from
// the deployment Secret at boot (booth/auth.BootstrapFromEnv) — there is no
// self-service account creation. "First run" therefore means: a freshly deployed
// Booth where the seed Admin has never signed in and has the secret-provisioned
// credential. The login surface uses this to show a welcome/setup variant that
// names the seed username and nudges a password rotation after first sign-in.
//
// Contract (implemented by the W6 admin API — see plan §3.2 "A1 first-run"):
//   GET /v1/auth/bootstrap-status → 200 { firstRun: boolean, adminUsername: string }
// Until W6 ships it the endpoint is absent; this hook degrades to firstRun:false
// on any non-200 (404/network/parse) so login works unchanged. The probe only
// runs when asked (the login page, while anonymous), so it costs nothing elsewhere.
export interface BootstrapStatus {
  firstRun: boolean;
  adminUsername: string;
}

const BOOTSTRAP_PATH = "/v1/auth/bootstrap-status";

export type BootstrapProbe =
  | { state: "loading" }
  | { state: "ready"; status: BootstrapStatus };

export function useBootstrapStatus(enabled: boolean): BootstrapProbe {
  const [probe, setProbe] = useState<BootstrapProbe>(
    enabled ? { state: "loading" } : { state: "ready", status: { firstRun: false, adminUsername: "" } },
  );

  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    (async () => {
      try {
        const status = await api.get<BootstrapStatus>(BOOTSTRAP_PATH);
        if (alive) {
          setProbe({
            state: "ready",
            status: { firstRun: !!status.firstRun, adminUsername: status.adminUsername ?? "" },
          });
        }
      } catch (err) {
        // Endpoint not yet deployed (W6) or unreachable → treat as not-first-run
        // so the standard login renders. A 401 here must NOT be read as an expired
        // session (nobody was signed in); api's central handler already no-ops
        // while anonymous, and we swallow it the same way.
        void (err instanceof ApiError);
        if (alive) {
          setProbe({ state: "ready", status: { firstRun: false, adminUsername: "" } });
        }
      }
    })();
    return () => {
      alive = false;
    };
  }, [enabled]);

  return probe;
}
