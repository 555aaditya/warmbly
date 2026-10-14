import type { PropsWithChildren } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import useEmails from "./useEmails";
import getEmails from "@/lib/api/client/app/emails/getEmails";
import type Inbox from "@/lib/api/models/app/emails/Inbox";
import type GetEmails from "@/lib/api/models/app/emails/GetEmails";

vi.mock("@/lib/api/client/app/emails/getEmails", () => ({ default: vi.fn() }));

function harness() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
    const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    return { client, wrapper };
}

function page(id: string, cursor: string | null): GetEmails {
    return {
        data: [{ id }] as Inbox[],
        pagination: { has_more: cursor !== null, next_cursor: cursor, total: 2 },
    };
}

beforeEach(() => vi.resetAllMocks());

describe("mailbox query lifecycle", () => {
    it("still completes all pages and refreshes both pages on realtime invalidation", async () => {
        const { client, wrapper } = harness();
        let revision = 0;
        vi.mocked(getEmails).mockImplementation(async (_query, cursor) => cursor ? page(`b-${revision}`, null) : page(`a-${revision}`, "next"));
        const { result, rerender, unmount } = renderHook(() => useEmails({ query: "", tag: "" }), { wrapper });
        await waitFor(() => expect(result.current.emails.map((r) => r.id)).toEqual(["a-0", "b-0"]));
        expect(getEmails).toHaveBeenCalledTimes(2);
        expect(getEmails).toHaveBeenNthCalledWith(1, "", null, "", 200);
        expect(getEmails).toHaveBeenNthCalledWith(2, "", "next", "", 200);
        const first = result.current.emails;
        rerender();
        expect(result.current.emails).toBe(first);
        expect(getEmails).toHaveBeenCalledTimes(2);
        revision++;
        act(() => { void client.invalidateQueries({ queryKey: ["emails"] }); });
        await waitFor(() => expect(result.current.emails.map((r) => r.id)).toEqual(["a-1", "b-1"]));
        expect(getEmails).toHaveBeenCalledTimes(4);
        expect(result.current.hasNextPage).toBe(false);
        unmount(); client.clear();
    });

    it("respects enabled gating and clears old rows when query or tag changes", async () => {
        const { client, wrapper } = harness();
        vi.mocked(getEmails).mockImplementation(async (query, _cursor, tag) => page(`${query}:${tag}`, null));
        const { result, rerender, unmount } = renderHook(
            (props) => useEmails(props),
            { wrapper, initialProps: { query: "first", tag: "a", enabled: false } },
        );
        expect(getEmails).not.toHaveBeenCalled();
        expect(result.current.emails).toEqual([]);
        rerender({ query: "first", tag: "a", enabled: true });
        await waitFor(() => expect(result.current.emails[0]?.id).toBe("first:a"));
        rerender({ query: "second", tag: "b", enabled: true });
        expect(result.current.emails).toEqual([]);
        await waitFor(() => expect(result.current.emails[0]?.id).toBe("second:b"));
        expect(getEmails).toHaveBeenCalledTimes(2);
        unmount(); client.clear();
    });

    it("keeps loaded rows and surfaces a next-page failure without looping requests", async () => {
        const { client, wrapper } = harness();
        const error = new Error("next page failed");
        vi.mocked(getEmails).mockResolvedValueOnce(page("a", "next")).mockRejectedValue(error);
        const { result, rerender, unmount } = renderHook(() => useEmails({ query: "", tag: "" }), { wrapper });
        await waitFor(() => expect(result.current.isFetchNextPageError).toBe(true));
        expect(result.current.error).toBe(error);
        expect(result.current.emails.map((r) => r.id)).toEqual(["a"]);
        for (let i = 0; i < 10; i++) rerender();
        expect(getEmails).toHaveBeenCalledTimes(2);
        unmount(); client.clear();
    });
});
