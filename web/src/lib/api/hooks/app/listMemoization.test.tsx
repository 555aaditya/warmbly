import { useMemo } from "react";
import type * as ReactQuery from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import useEmails from "@/lib/api/hooks/app/emails/useEmails";
import useCampaigns from "@/lib/api/hooks/app/campaigns/useCampaigns";
import useSearchContacts from "@/lib/api/hooks/app/contacts/useSearchContacts";
import useUniboxSearch from "@/lib/api/hooks/app/unibox/useUniboxSearch";
import useSearchTasks from "@/lib/api/hooks/app/crm/tasks/useSearchTasks";
import useSearchDeals from "@/lib/api/hooks/app/crm/deals/useSearchDeals";
import useSearchMeetings from "@/lib/api/hooks/app/meetings/useSearchMeetings";
import { EMPTY_TASK_SEARCH } from "@/lib/api/models/app/crm/SearchTasks";
import { EMPTY_DEAL_SEARCH } from "@/lib/api/models/app/crm/SearchDeals";
import type SearchContacts from "@/lib/api/models/app/contacts/SearchContacts";

type Row = { id: string };
type Page = { data?: (Row | null)[] | null; pagination: { total: number } };

const state = vi.hoisted(() => ({
    query: {
        data: undefined as { pages: Page[] } | undefined,
        isFetching: false,
        isError: false,
        error: null as Error | null,
    },
}));

vi.mock("@tanstack/react-query", async (importOriginal) => ({
    ...await importOriginal<typeof ReactQuery>(),
    useInfiniteQuery: () => state.query,
}));
vi.mock("@/lib/api/hooks/useAllPages", () => ({ default: vi.fn() }));
vi.mock("@/hooks/useRealtimeFallback", () => ({ default: () => false }));

const contactsOptions: SearchContacts = {
    query: "", custom_field_filters: [], campaign_ids: [], sort_by: "updated_at", reverse: false,
};

const lists: { name: string; useList: () => Row[] | undefined; unloaded: Row[] | undefined }[] = [
    { name: "mailboxes", useList: () => useEmails({ query: "", tag: "" }).emails, unloaded: [] },
    { name: "campaigns", useList: () => useCampaigns({ query: "", folder: "" }).campaigns, unloaded: [] },
    { name: "contacts", useList: () => useSearchContacts({ options: contactsOptions }).contacts, unloaded: undefined },
    { name: "unibox", useList: () => useUniboxSearch({}, "all").emails, unloaded: [] },
    { name: "tasks", useList: () => useSearchTasks({ filters: EMPTY_TASK_SEARCH }).tasks, unloaded: undefined },
    { name: "deals", useList: () => useSearchDeals({ filters: EMPTY_DEAL_SEARCH }).deals, unloaded: undefined },
    { name: "meetings", useList: () => useSearchMeetings({ filters: {} }).meetings, unloaded: undefined },
];

function page(data: Page["data"]): Page {
    return { data, pagination: { total: data?.length ?? 0 } };
}

beforeEach(() => {
    state.query = { data: undefined, isFetching: false, isError: false, error: null };
});

describe.each(lists)("$name list", ({ name, useList, unloaded }) => {
    it("keeps the unloaded shape and loaded row identities/order", () => {
        const { result, rerender } = renderHook(useList);
        expect(result.current).toEqual(unloaded);
        const empty = result.current;
        rerender();
        expect(result.current).toBe(empty);
        const a = { id: "a" }, b = { id: "b" };
        state.query.data = { pages: [page([a]), page([b])] };
        rerender();
        expect(result.current).toEqual([a, b]);
        expect(result.current?.[0]).toBe(a);
        expect(result.current?.[1]).toBe(b);
    });

    it("does not traverse loaded pages on unrelated renders or fetch/error flag changes", () => {
        const read = vi.fn(() => [{ id: "a" }]);
        state.query.data = { pages: [{ get data() { return read(); }, pagination: { total: 1 } }] };
        const { result, rerender } = renderHook(useList);
        const first = result.current;
        expect(read).toHaveBeenCalledTimes(1);
        for (let i = 0; i < 10; i++) {
            state.query = { ...state.query, isFetching: i % 2 === 0 };
            rerender();
            expect(result.current).toBe(first);
        }
        state.query = { ...state.query, isError: true, error: new Error("refetch failed") };
        rerender();
        expect(result.current).toBe(first);
        expect(read).toHaveBeenCalledTimes(1);
    });

    it("recomputes on append, changed row, removal and cache reset", () => {
        const a = { id: "a" }, b = { id: "b" };
        state.query.data = { pages: [page([a])] };
        const { result, rerender } = renderHook(useList);
        state.query.data = { pages: [...state.query.data.pages, page([b])] };
        rerender();
        expect(result.current).toEqual([a, b]);
        const updated = { id: "a", title: "refreshed" };
        state.query.data = { pages: [page([updated]), page([b])] };
        rerender();
        expect(result.current?.[0]).toBe(updated);
        state.query.data = { pages: [page([b])] };
        rerender();
        expect(result.current).toEqual([b]);
        state.query.data = undefined;
        rerender();
        expect(result.current).toEqual(unloaded);
    });

    if (name !== "mailboxes") {
        it("preserves defensive null and missing payload handling", () => {
            state.query.data = { pages: [page(null), page(undefined), page([{ id: "a" }, null])] };
            const { result } = renderHook(useList);
            expect(result.current).toEqual([{ id: "a" }]);
        });
    }
});

it("measures repeated list work with 4,000 mailboxes and periodic immutable refreshes", () => {
    let pageReads = 0, sorts = 0;
    const rows = Array.from({ length: 4_000 }, (_, i) => ({ id: String(i).padStart(5, "0") }));
    function snapshot() {
        return {
            pages: Array.from({ length: 20 }, (_, i) => {
                const data = rows.slice(i * 200, (i + 1) * 200);
                return {
                    get data() { pageReads++; return data; },
                    pagination: { total: rows.length },
                };
            }),
        };
    }
    state.query.data = snapshot();
    const start = performance.now();
    const { result, rerender, unmount } = renderHook(() => {
        const mailboxes = useEmails({ query: "", tag: "" }).emails;
        return useMemo(() => {
            sorts++;
            return [...mailboxes].sort((a, b) => b.id.localeCompare(a.id));
        }, [mailboxes]);
    });
    for (let i = 1; i <= 200; i++) {
        if (i % 20 === 0) state.query.data = snapshot();
        rerender();
    }
    expect(result.current).toHaveLength(4_000);
    expect(result.current[0].id).toBe("03999");
    unmount();
    console.info(JSON.stringify({ fixture: "4,000 mailboxes; initial + 200 renders; 10 refreshes", pageReads, sorts, elapsedMs: performance.now() - start }));
    expect(pageReads).toBe(220);
    expect(sorts).toBe(11);
});
