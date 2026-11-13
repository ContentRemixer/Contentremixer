import { createContext, useContext, useState } from "react";
import { Navigate, Outlet, useLocation } from "react-router-dom";
import { useAuth } from "../../app/auth";
import { useOnboardingStatus } from "../../app/onboarding";
import { FullScreenLoader } from "../../components/Loader";

// OnboardingGate sits between RequireAuth and the authed layouts. It runs the D5
// first-run probe once (ISI-7360 /v1/onboarding-status) and, for a DevRel who has
// no validated persona yet, routes them into the wizard (/onboarding) before the
// normal shell. A probe failure degrades to needsOnboarding:false (handled in the
// hook), so a backend hiccup can never trap a user in onboarding.
//
// The gate is deliberately a ONE-SHOT redirect, not a lock: once the user is in
// the wizard we don't keep forcing them, and when they finish (markDone) we let
// them leave without the stale probe bouncing them back.

const WIZARD_PATH = "/onboarding";

interface OnboardingGateApi {
  markDone: () => void;
}

const GateContext = createContext<OnboardingGateApi | null>(null);

// useOnboardingGate lets the wizard mark onboarding complete so the gate stops
// redirecting into it after the persona is validated.
export function useOnboardingGate(): OnboardingGateApi {
  const ctx = useContext(GateContext);
  if (!ctx) throw new Error("useOnboardingGate must be used within OnboardingGate");
  return ctx;
}

export function OnboardingGate() {
  const { status, session } = useAuth();
  const location = useLocation();
  // Only probe for an authenticated session; Admins never need onboarding.
  const enabled = status === "authenticated" && session?.role === "DevRel";
  const probe = useOnboardingStatus(enabled);
  // Set once the wizard validates a persona (or the user opts out), so neither
  // redirect fires afterwards.
  const [done, setDone] = useState(false);

  if (enabled && probe.state === "loading") {
    return <FullScreenLoader label="Checking your workspace…" />;
  }

  const needsOnboarding = enabled && probe.state === "ready" && probe.status.needsOnboarding && !done;
  const onWizard = location.pathname.startsWith(WIZARD_PATH);

  // First-run DevRel who hasn't started: send them to the wizard.
  if (needsOnboarding && !onWizard) {
    return <Navigate to={WIZARD_PATH} replace />;
  }
  // Already set up (or finished) but landed on /onboarding: send them home.
  if (!needsOnboarding && onWizard && !done) {
    return <Navigate to="/" replace />;
  }

  return (
    <GateContext.Provider value={{ markDone: () => setDone(true) }}>
      <Outlet />
    </GateContext.Provider>
  );
}
