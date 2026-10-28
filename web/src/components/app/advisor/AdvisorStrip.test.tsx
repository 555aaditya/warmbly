import type { ComponentProps, ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { AdvisorFinding } from "@/lib/api/models/app/advisor/Advisor";
import type AdvisorCard from "./AdvisorCard";
import type AdvisorFixDrawer from "./AdvisorFixDrawer";
import AdvisorStrip from "./AdvisorStrip";

const query = vi.hoisted((): { data: AdvisorFinding[] } => ({ data: [] }));

vi.mock("@/lib/api/hooks/app/advisor/useAdvisor", () => ({
    SURFACE_FETCH_LIMIT: 200,
    useAdvisorFindings: () => query,
}));

vi.mock("./AdvisorCard", () => ({
    default: ({ finding, onFix }: ComponentProps<typeof AdvisorCard>) => (
        <button onClick={() => onFix(finding)}>{finding.title}</button>
    ),
}));

vi.mock("./AdvisorFixDrawer", () => ({
    default: ({ onClose }: ComponentProps<typeof AdvisorFixDrawer>) => (
        <div role="dialog" aria-label="Fix result">
            <button onClick={onClose}>Close result</button>
        </div>
    ),
}));

vi.mock("framer-motion", async (importOriginal) => ({
    ...(await importOriginal<Record<string, unknown>>()),
    AnimatePresence: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

afterEach(cleanup);

it("removes the last suggestion while keeping its fix result available", () => {
    query.data = [{
        id: "finding", organization_id: "workspace", detector_key: "campaign_followup_spacing",
        category: "campaign", severity: "low", surface: "campaigns", status: "open", impact: 15,
        title: "Review follow-up spacing", detail: "Too soon", remedy: "Wait three days",
        narrated: false, first_seen_at: new Date(), last_seen_at: new Date(),
    }];
    const view = render(<AdvisorStrip entityType="campaign" entityId="campaign" />);
    fireEvent.click(screen.getByRole("button", { name: "Review follow-up spacing" }));

    query.data = [];
    view.rerender(<AdvisorStrip entityType="campaign" entityId="campaign" />);

    expect(screen.queryByRole("region", { name: "Advisor suggestions" })).toBeNull();
    expect(screen.getByRole("dialog", { name: "Fix result" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Close result" }));
    expect(screen.queryByRole("dialog")).toBeNull();
});
