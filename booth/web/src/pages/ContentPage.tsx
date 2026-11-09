import { Routes, Route, Navigate } from "react-router-dom";
import { ContentListPage } from "./content/ContentListPage";
import { ContentDetailPage } from "./content/ContentDetailPage";

// Content surface (B3, plan §3.2): the document index and the content-detail
// reader with inline comments, peer-review threads, and the request-illustration
// flow. Mounted at /content/* by the app router; server-side §6.3 authz is
// authoritative, so every underlying call is gated regardless of this routing.
export function ContentPage() {
  return (
    <Routes>
      <Route index element={<ContentListPage />} />
      <Route path=":id" element={<ContentDetailPage />} />
      <Route path="*" element={<Navigate to="/content" replace />} />
    </Routes>
  );
}
