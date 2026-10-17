import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { createDeliveryDeduper, createHiddenTabGate, createRefreshCoalescer } from "./refreshCoalescer";

describe("createRefreshCoalescer", () => {
    let client: QueryClient;

    beforeEach(() => {
        vi.useFakeTimers();
        client = new QueryClient();
    });
    afterEach(() => {
        vi.useRealTimers();
        client.clear();
    });

    it("turns a burst into one invalidation after it goes quiet", () => {
        const spy = vi.spyOn(client, "invalidateQueries");
        const c = createRefreshCoalescer(client, { delayMs: 1000, maxWaitMs: 5000 });
        for (let i = 0; i < 20; i++) {
            c.add([["unibox", "overview"], ["analytics"]]);
            vi.advanceTimersByTime(100);
        }
        expect(spy).not.toHaveBeenCalled();
        vi.advanceTimersByTime(1000);
        expect(spy).toHaveBeenCalledTimes(2);
    });

    it("still refreshes during a burst that never pauses", () => {
        const spy = vi.spyOn(client, "invalidateQueries");
        const c = createRefreshCoalescer(client, { delayMs: 1000, maxWaitMs: 3000 });
        for (let i = 0; i < 35; i++) {
            c.add([["unibox", "overview"]]);
            vi.advanceTimersByTime(100);
        }
        expect(spy).toHaveBeenCalledTimes(1);
        c.dispose();
    });

    it("follows a flush forced by the cap with a trailing one for keys added just before it", () => {
        const spy = vi.spyOn(client, "invalidateQueries");
        const c = createRefreshCoalescer(client, { delayMs: 1000, maxWaitMs: 3000 });
        c.add([["analytics"]]);
        for (let i = 0; i < 30; i++) {
            c.add([["unibox", "overview"]]);
            vi.advanceTimersByTime(100);
        }
        // The cap fired at 3000, 100ms after the last add.
        expect(spy.mock.calls.map((call) => call[0]?.queryKey)).toEqual([["analytics"], ["unibox", "overview"]]);
        vi.advanceTimersByTime(999);
        expect(spy).toHaveBeenCalledTimes(2);
        vi.advanceTimersByTime(1);
        expect(spy).toHaveBeenCalledTimes(3);
        expect(spy.mock.calls[2][0]).toMatchObject({ queryKey: ["unibox", "overview"] });
        vi.advanceTimersByTime(5000);
        expect(spy).toHaveBeenCalledTimes(3);
    });

    it("skips a key a broader pending key already covers", () => {
        const spy = vi.spyOn(client, "invalidateQueries");
        const c = createRefreshCoalescer(client, { delayMs: 10 });
        c.add([["unibox", "overview"], ["unibox"], ["unibox", "search"]]);
        vi.advanceTimersByTime(10);
        expect(spy).toHaveBeenCalledTimes(1);
        expect(spy.mock.calls[0][0]).toMatchObject({ queryKey: ["unibox"] });
    });

    it("leaves a running fetch alone and re-checks it next round", async () => {
        let resolve!: (v: number) => void;
        void client.fetchQuery({ queryKey: ["unibox", "overview"], queryFn: () => new Promise<number>((r) => (resolve = r)) });
        const spy = vi.spyOn(client, "invalidateQueries");
        const c = createRefreshCoalescer(client, { delayMs: 10 });
        c.add([["unibox", "overview"]]);
        vi.advanceTimersByTime(10);
        expect(spy).toHaveBeenCalledTimes(1);
        expect(client.getQueryState(["unibox", "overview"])?.isInvalidated).toBe(false);

        resolve(1);
        await vi.advanceTimersByTimeAsync(10);
        expect(spy).toHaveBeenCalledTimes(2);
        expect(client.getQueryState(["unibox", "overview"])?.isInvalidated).toBe(true);
    });
});

describe("createRefreshCoalescer with a match", () => {
    let client: QueryClient;

    beforeEach(() => {
        vi.useFakeTimers();
        client = new QueryClient();
        for (const key of [["contacts", "list"], ["contacts", "a", "campaign-state"], ["contacts", "b", "campaign-state"], ["contacts", "c", "campaign-state"]]) {
            client.setQueryData(key, 1);
        }
    });
    afterEach(() => {
        vi.useRealTimers();
        client.clear();
    });

    const invalidated = () =>
        client.getQueryCache().getAll().filter((q) => q.state.isInvalidated).map((q) => q.queryKey.join("/")).sort();

    it("refreshes the union of what each narrowed add asked for", () => {
        const c = createRefreshCoalescer(client, { delayMs: 10 });
        const notOtherState = (id: string) => (q: { queryKey: readonly unknown[] }) => q.queryKey[2] !== "campaign-state" || q.queryKey[1] === id;
        c.add([["contacts"]], notOtherState("a"));
        c.add([["contacts"]], notOtherState("b"));
        vi.advanceTimersByTime(10);
        expect(invalidated()).toEqual(["contacts/a/campaign-state", "contacts/b/campaign-state", "contacts/list"]);
    });

    it("lets an unnarrowed add widen a narrowed one", () => {
        const c = createRefreshCoalescer(client, { delayMs: 10 });
        c.add([["contacts"]], (q) => q.queryKey[1] === "list");
        c.add([["contacts"]]);
        vi.advanceTimersByTime(10);
        expect(invalidated()).toHaveLength(4);
    });

    it("does not treat a narrowed broader key as covering a narrower one", () => {
        const c = createRefreshCoalescer(client, { delayMs: 10 });
        c.add([["contacts"]], (q) => q.queryKey[1] === "list");
        c.add([["contacts", "c"]]);
        vi.advanceTimersByTime(10);
        expect(invalidated()).toEqual(["contacts/c/campaign-state", "contacts/list"]);
    });
});

describe("createHiddenTabGate", () => {
    let client: QueryClient;
    let visibility: DocumentVisibilityState;

    const setVisibility = (state: DocumentVisibilityState) => {
        visibility = state;
        document.dispatchEvent(new Event("visibilitychange"));
    };
    const observe = (key: string[]) => {
        const fn = vi.fn(async () => 1);
        const observer = new QueryObserver(client, { queryKey: key, queryFn: fn });
        const unsubscribe = observer.subscribe(() => {});
        return { fn, unsubscribe };
    };

    beforeEach(() => {
        client = new QueryClient();
        visibility = "visible";
        vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
    });
    afterEach(() => {
        vi.restoreAllMocks();
        client.clear();
    });

    it("refetches at once on a visible tab", async () => {
        const gate = createHiddenTabGate(client);
        const list = observe(["campaigns", "list"]);
        await vi.waitFor(() => expect(client.getQueryData(["campaigns", "list"])).toBe(1));
        gate.invalidate({ queryKey: ["campaigns"] });
        await vi.waitFor(() => expect(list.fn).toHaveBeenCalledTimes(2));
        list.unsubscribe();
        gate.dispose();
    });

    it("defers a hidden tab's refetches to one on return, keeping the unread badge live", async () => {
        const gate = createHiddenTabGate(client);
        const list = observe(["campaigns", "list"]);
        const badge = observe(["unibox", "unseen-count"]);
        const overview = observe(["unibox", "overview"]);
        await vi.waitFor(() => expect(client.isFetching()).toBe(0));

        setVisibility("hidden");
        for (let i = 0; i < 5; i++) {
            gate.invalidate({ queryKey: ["campaigns", "list"] });
            gate.invalidate({ queryKey: ["unibox"] });
        }
        await vi.waitFor(() => expect(badge.fn).toHaveBeenCalledTimes(6));
        expect(list.fn).toHaveBeenCalledTimes(1);
        expect(overview.fn).toHaveBeenCalledTimes(1);
        expect(client.getQueryState(["campaigns", "list"])?.isInvalidated).toBe(true);

        setVisibility("visible");
        await vi.waitFor(() => expect(list.fn).toHaveBeenCalledTimes(2));
        await vi.waitFor(() => expect(overview.fn).toHaveBeenCalledTimes(2));
        setVisibility("hidden");
        setVisibility("visible");
        await Promise.resolve();
        expect(list.fn).toHaveBeenCalledTimes(2);
        for (const o of [list, badge, overview]) o.unsubscribe();
        gate.dispose();
    });
});

describe("createDeliveryDeduper", () => {
    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    it("drops the second copy of a delivery inside the window only", () => {
        const seen = createDeliveryDeduper(1000);
        expect(seen("EMAIL_RECEIVED|t1|m1")).toBe(false);
        expect(seen("EMAIL_RECEIVED|t1|m1")).toBe(true);
        expect(seen("EMAIL_UPDATED|t1|m1")).toBe(false);
        vi.advanceTimersByTime(1000);
        expect(seen("EMAIL_RECEIVED|t1|m1")).toBe(false);
    });
});
