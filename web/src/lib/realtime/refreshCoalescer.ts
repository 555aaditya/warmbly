// Bounds realtime refetches: one invalidation per quiet interval (capped), and one handling per delivered event.

import {
    hashKey,
    partialMatchKey,
    type InvalidateOptions,
    type InvalidateQueryFilters,
    type Query,
    type QueryClient,
    type QueryKey,
} from "@tanstack/react-query";

export type QueryMatch = (query: Query) => boolean;
export type Invalidate = (filters: InvalidateQueryFilters, options?: InvalidateOptions) => void;

export interface RefreshCoalescer {
    /** Queues keys (prefixes) for the next flush, optionally narrowed to the queries `match` accepts. */
    add(keys: readonly QueryKey[], match?: QueryMatch): void;
    /** Drops anything pending without invalidating it. */
    dispose(): void;
}

export interface RefreshCoalescerOptions {
    /** Quiet time after the last add before the flush. */
    delayMs?: number;
    /** Longest a key may wait while adds keep arriving. */
    maxWaitMs?: number;
    /** Runs each flushed invalidation; defaults to the client's own. */
    invalidate?: Invalidate;
}

// Kept above the backend's overviewFreshFor (internal/app/unibox/overview_cache.go).
export const REFRESH_DELAY_MS = 1500;
export const REFRESH_MAX_WAIT_MS = 5000;

interface Pending {
    key: QueryKey;
    match?: QueryMatch;
}

export function createRefreshCoalescer(
    queryClient: QueryClient,
    {
        delayMs = REFRESH_DELAY_MS,
        maxWaitMs = REFRESH_MAX_WAIT_MS,
        invalidate = (filters, options) => void queryClient.invalidateQueries(filters, options),
    }: RefreshCoalescerOptions = {},
): RefreshCoalescer {
    // Each pending key with the time it was last added.
    const pending = new Map<string, Pending & { at: number }>();
    let timer: ReturnType<typeof setTimeout> | null = null;
    let firstAt = 0;

    const schedule = () => {
        const now = Date.now();
        if (timer) clearTimeout(timer);
        else firstAt = now;
        timer = setTimeout(flush, Math.max(0, Math.min(delayMs, firstAt + maxWaitMs - now)));
    };

    const queue = (entries: readonly Pending[]) => {
        if (entries.length === 0) return;
        const now = Date.now();
        for (const { key, match } of entries) {
            const hash = hashKey(key);
            const prev = pending.get(hash);
            const prevMatch = prev?.match;
            // Two narrowed adds of one key refresh what either asked for; an unnarrowed one refreshes everything.
            const merged = !prev ? match : prevMatch && match ? (q: Query) => prevMatch(q) || match(q) : undefined;
            pending.set(hash, { key, match: merged, at: now });
        }
        schedule();
    };

    const add = (keys: readonly QueryKey[], match?: QueryMatch) => queue(keys.map((key) => ({ key, match })));

    const flush = () => {
        timer = null;
        const now = Date.now();
        const entries = [...pending.values()];
        pending.clear();
        // A flush forced by the cap may beat the last event's writes, so recent keys get a trailing flush too.
        const again: Pending[] = entries.filter((e) => now - e.at < delayMs).map(({ key, match }) => ({ key, match }));
        for (const { key, match } of entries) {
            // A broader pending key already covers this one.
            if (entries.some((o) => o.key !== key && !o.match && o.key.length < key.length && partialMatchKey(key, o.key))) {
                continue;
            }
            // A running fetch finishes rather than restarts, and is re-checked next round since it may predate the event.
            for (const query of queryClient.getQueryCache().findAll({ queryKey: key, fetchStatus: "fetching", predicate: match })) {
                again.push({ key: query.queryKey });
            }
            invalidate(
                { queryKey: key, predicate: (query) => query.state.fetchStatus !== "fetching" && (!match || match(query)) },
                { cancelRefetch: false },
            );
        }
        queue(again);
    };

    return {
        add,
        dispose: () => {
            if (timer) clearTimeout(timer);
            timer = null;
            pending.clear();
        },
    };
}

// Shown on a hidden tab (its title and favicon badge), so these keep refetching there.
const HIDDEN_TAB_LIVE_KEYS: readonly QueryKey[] = [["unibox", "unseen-count"]];

export interface HiddenTabGate {
    /** Invalidates now; on a hidden tab only marks stale, and refetches once the tab is visible again. */
    invalidate: Invalidate;
    /** Forgets deferred refetches and stops listening. */
    dispose(): void;
}

export function createHiddenTabGate(queryClient: QueryClient): HiddenTabGate {
    // Hashes of the active queries a hidden tab left stale.
    const deferred = new Set<string>();
    let listening = false;

    const hidden = () => typeof document !== "undefined" && document.visibilityState === "hidden";
    const live: QueryMatch = (query) => HIDDEN_TAB_LIVE_KEYS.some((key) => partialMatchKey(query.queryKey, key));

    // On document, so it runs before the focus manager's window listener and a focus refetch joins this one.
    const onVisibility = () => {
        if (hidden()) return;
        stop();
        if (deferred.size === 0) return;
        const hashes = new Set(deferred);
        deferred.clear();
        void queryClient.refetchQueries({ type: "active", predicate: (query) => hashes.has(query.queryHash) });
    };
    const stop = () => {
        if (!listening) return;
        document.removeEventListener("visibilitychange", onVisibility);
        listening = false;
    };

    const invalidate: Invalidate = (filters, options) => {
        if (!hidden()) {
            void queryClient.invalidateQueries(filters, options);
            return;
        }
        const match = filters.predicate;
        const stale: InvalidateQueryFilters = { ...filters, predicate: (q) => !live(q) && (!match || match(q)) };
        for (const query of queryClient.getQueryCache().findAll({ ...stale, type: "active" })) deferred.add(query.queryHash);
        void queryClient.invalidateQueries({ ...stale, refetchType: "none" });
        void queryClient.invalidateQueries({ ...filters, predicate: (q) => live(q) && (!match || match(q)) }, options);
        if (!listening) {
            document.addEventListener("visibilitychange", onVisibility);
            listening = true;
        }
    };

    return {
        invalidate,
        dispose: () => {
            stop();
            deferred.clear();
        },
    };
}

/**
 * Returns true when the same delivery was already seen within `windowMs`. The
 * key should name the event and its publish (type, timestamp, ids), so a
 * genuine second change is never mistaken for a copy.
 */
export function createDeliveryDeduper(windowMs = 2000): (key: string) => boolean {
    const seen = new Map<string, number>();
    return (key) => {
        const now = Date.now();
        // Insertion order is arrival order, so expired entries are at the front.
        for (const [k, at] of seen) {
            if (now - at < windowMs) break;
            seen.delete(k);
        }
        if (seen.has(key)) return true;
        seen.set(key, now);
        return false;
    };
}
