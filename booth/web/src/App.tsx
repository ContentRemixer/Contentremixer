import { Routes, Route, Navigate } from "react-router-dom";
import { AppShell } from "./shell/AppShell";
import { RequireAuth } from "./routes/RequireAuth";
import { FullScreenLoader } from "./components/Loader";
import { useAuth } from "./app/auth";
import { LoginPage } from "./pages/LoginPage";
import { HomePage } from "./pages/HomePage";
import { ContentPage } from "./pages/ContentPage";
import { PersonaPage } from "./pages/personas/PersonaPage";
import { ScraperReviewPage } from "./pages/ScraperReviewPage";
import { AdminPage } from "./pages/AdminPage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { ForbiddenPage } from "./pages/ForbiddenPage";
import { StatesPage } from "./pages/StatesPage";
import { OnboardingGate } from "./pages/onboarding/gate";
import { OnboardingWizard } from "./pages/OnboardingWizard";

// Route table. Server-side authz is authoritative (§4); client routing is
// cosmetic — RequireAuth only gates on session presence, and the admin branch is
// hidden from DevRel nav but still 403'd by the server if hit directly.
export default function App() {
  const { status } = useAuth();

  if (status === "loading") {
    return <FullScreenLoader label="Starting ContentRemixer…" />;
  }

  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />

      {/* Authenticated area. OnboardingGate runs the D5 first-run probe and, for
          a DevRel without a validated persona, routes to the /onboarding wizard
          (ISI-7361 D6) before the normal shell. */}
      <Route
        element={
          <RequireAuth>
            <OnboardingGate />
          </RequireAuth>
        }
      >
        {/* First-run wizard: full-screen, outside the AppShell chrome. */}
        <Route path="onboarding" element={<OnboardingWizard />} />

        <Route element={<AppShell />}>
          <Route index element={<HomePage />} />
          <Route path="content/*" element={<ContentPage />} />
          <Route path="personas/*" element={<PersonaPage />} />
          <Route path="scraper-review/*" element={<ScraperReviewPage />} />
          <Route path="admin/*" element={<RequireAuth role="Admin"><AdminPage /></RequireAuth>} />
          <Route path="states" element={<StatesPage />} />
          <Route path="403" element={<ForbiddenPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
