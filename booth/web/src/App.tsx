import { Routes, Route, Navigate } from "react-router-dom";
import { AppShell } from "./shell/AppShell";
import { RequireAuth } from "./routes/RequireAuth";
import { FullScreenLoader } from "./components/Loader";
import { useAuth } from "./app/auth";
import { LoginPage } from "./pages/LoginPage";
import { HomePage } from "./pages/HomePage";
import { ContentPage } from "./pages/ContentPage";
import { ScraperReviewPage } from "./pages/ScraperReviewPage";
import { AdminPage } from "./pages/AdminPage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { ForbiddenPage } from "./pages/ForbiddenPage";

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

      <Route
        element={
          <RequireAuth>
            <AppShell />
          </RequireAuth>
        }
      >
        <Route index element={<HomePage />} />
        <Route path="content/*" element={<ContentPage />} />
        <Route path="scraper-review/*" element={<ScraperReviewPage />} />
        <Route path="admin/*" element={<RequireAuth role="Admin"><AdminPage /></RequireAuth>} />
        <Route path="403" element={<ForbiddenPage />} />
        <Route path="*" element={<NotFoundPage />} />
      </Route>

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
