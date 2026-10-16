import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ComponentProps, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { UserContext } from "@/hooks/context/user";
import { useAppStore } from "@/stores/useAppStore";
import { BROWSE_STORAGE_PREFIX } from "@/lib/browseState";
import type ContactCampaignState from "@/lib/api/models/app/contacts/ContactCampaignState";
import ActivityTab from "./ActivityTab";

const timeline = vi.hoisted(() => ({ events: [], isLoading: false, hasNextPage: false, fetchNextPage: vi.fn() }));
const campaignStates = vi.hoisted(() => ({ data: { data: [] as ContactCampaignState[] } }));
vi.mock("@/lib/api/hooks/app/contacts/useContactTimeline", () => ({ default: () => timeline }));
vi.mock("@/lib/api/hooks/app/contacts/useContactCampaignStates", () => ({ default: () => campaignStates }));
vi.mock("@/components/ui/DatePicker", () => ({ DatePicker: ({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder: string }) =>
    <input placeholder={placeholder} value={value} onChange={(e) => onChange(e.target.value)} /> }));

function Owner({ children }: { children: ReactNode }) {
    const value = { user: { id: "member" } } as ComponentProps<typeof UserContext.Provider>["value"];
    return (
        <QueryClientProvider client={new QueryClient()}>
            <UserContext.Provider value={value}>{children}</UserContext.Provider>
        </QueryClientProvider>
    );
}
const query = () => screen.getByPlaceholderText("Search subject, content, campaign…");
const saved = () => JSON.parse(sessionStorage.getItem(`${BROWSE_STORAGE_PREFIX}member:workspace:contacts:a:activity:filters`)!).value;

describe("contact activity browsing", () => {
    beforeEach(() => {
        campaignStates.data.data = [];
        sessionStorage.clear();
        useAppStore.setState({ currentOrganization: { id: "workspace", name: "Workspace", role: "owner" } });
    });
    it("restores query, type and date filters, isolates contacts, and retains intentional reset", () => {
        const page = render(<ActivityTab contactId="a" contactName="Alice" />, { wrapper: Owner });
        fireEvent.change(query(), { target: { value: "proposal" } });
        fireEvent.click(screen.getByRole("button", { name: "Replies" }));
        fireEvent.click(screen.getByRole("button", { name: "Any date" }));
        fireEvent.change(screen.getByPlaceholderText("From"), { target: { value: "2026-10-01" } });
        fireEvent.change(screen.getByPlaceholderText("To"), { target: { value: "2026-10-07" } });
        expect(saved()).toEqual({ query: "proposal", type: "replies", from: "2026-10-01", to: "2026-10-07" });
        page.unmount();
        const refreshed = render(<ActivityTab contactId="a" contactName="Alice" />, { wrapper: Owner });
        expect(query()).toHaveValue("proposal");
        expect(screen.queryByRole("button", { name: "Any date" })).not.toBeInTheDocument();
        refreshed.rerender(<ActivityTab contactId="b" contactName="Bob" />);
        expect(query()).toHaveValue("");
        expect(screen.getByRole("button", { name: "Any date" })).toBeInTheDocument();
        refreshed.rerender(<ActivityTab contactId="a" contactName="Alice" />);
        expect(query()).toHaveValue("proposal");
        fireEvent.click(screen.getByRole("button", { name: "Reset" }));
        expect(saved()).toEqual({ query: "", type: "all", from: "", to: "" });
        refreshed.unmount();
        render(<ActivityTab contactId="a" contactName="Alice" />, { wrapper: Owner });
        expect(query()).toHaveValue("");
        expect(screen.queryByRole("button", { name: "Reset" })).not.toBeInTheDocument();
    });
});

describe("contact campaign steps", () => {
    beforeEach(() => {
        sessionStorage.clear();
        useAppStore.setState({ currentOrganization: { id: "workspace", name: "Workspace", role: "owner" } });
    });

    function campaign(steps: ContactCampaignState["steps"] | null | undefined): ContactCampaignState {
        return {
            campaign_id: "campaign",
            campaign_name: "Draft outreach",
            campaign_status: "draft",
            lead_status: "pending",
            // Older backends serialize an empty sequence as null.
            steps: steps as ContactCampaignState["steps"],
            completed_steps: 0,
            total_steps: steps?.length ?? 0,
        };
    }

    it.each([null, undefined, []])("renders and expands a campaign with %s steps", (steps) => {
        campaignStates.data.data = [campaign(steps)];
        render(<ActivityTab contactId="a" contactName="Alice" />, { wrapper: Owner });

        const card = screen.getByRole("button", { name: /Draft outreach.*0\/0 steps/ });
        expect(screen.getByText("Not started")).toBeInTheDocument();
        expect(screen.getByText("Nothing yet")).toBeInTheDocument();
        fireEvent.click(card);
        expect(card).toHaveAttribute("aria-expanded", "true");
        expect(query()).toBeInTheDocument();
    });

    it("keeps the progress strip and expanded rows for a populated campaign", () => {
        campaignStates.data.data = [campaign([{
            id: "step",
            label: "Email 1",
            kind: "email",
            position: 0,
            subject: "Hello Alice",
        }])];
        render(<ActivityTab contactId="a" contactName="Alice" />, { wrapper: Owner });

        expect(screen.getByTitle("Email 1")).toBeInTheDocument();
        expect(screen.queryByText("Email 1")).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: /Draft outreach.*0\/1 steps/ }));
        expect(screen.getByText("Email 1")).toBeInTheDocument();
        expect(screen.getByText("· Hello Alice")).toBeInTheDocument();
    });
});
