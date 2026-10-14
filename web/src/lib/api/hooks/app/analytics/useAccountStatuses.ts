import { useMemo } from "react";
import { useQueries, type UseQueryResult } from "@tanstack/react-query";
import getAccountStatuses from "@/lib/api/client/app/analytics/getAccountStatuses";
import type AccountStatus from "@/lib/api/models/app/analytics/AccountStatus";

// The accounts page asks only for the mailbox ids it currently shows, in
// bounded chunks, so the status read scales with the visible page and not with
// the whole inventory. Each chunk is its own query keyed by its id set and
// stays under the ["analytics","accounts",…] prefix the realtime layer
// invalidates, so account/warmup events still refresh every row live.
//
// The chunk size stays under the backend's email_ids ceiling
// (config.AccountStatusMaxIDs = 200).
const CHUNK = 100;

function chunk(ids: string[]): string[][] {
    const out: string[][] = [];
    for (let i = 0; i < ids.length; i += CHUNK) out.push(ids.slice(i, i + CHUNK));
    return out;
}

export default function useAccountStatuses(emailIds: string[], options?: { enabled?: boolean; sourceRevision?: number }) {
    // Sort so a chunk's id set (and its query key) is stable regardless of the
    // order the mailbox list arrived in; adding a mailbox only refetches the
    // chunk it lands in.
    const enabled = options?.enabled ?? true;
    const sourceRevision = options?.sourceRevision ?? 0;
    const queries = useMemo(
        () => chunk([...emailIds].sort()).map((ids) => ({
            queryKey: ["analytics", "accounts", "list", ids, sourceRevision],
            queryFn: ({ signal }: { signal: AbortSignal }) => getAccountStatuses(ids, signal),
            enabled: ids.length > 0 && enabled,
        })),
        [emailIds, enabled, sourceRevision],
    );

    return useQueries({ queries, combine: combineAccountStatuses });
}

// TanStack reuses the aggregation only while both results and this callback are stable.
function combineAccountStatuses(results: UseQueryResult<AccountStatus[], Error>[]) {
    return {
        data: results.flatMap((r) => r.data ?? []),
        observations: results.flatMap((r) => (r.data ?? []).map((status) => ({ status, observedAt: r.dataUpdatedAt, current: r.isSuccess && !r.isFetching }))),
        isLoading: results.some((r) => r.isLoading),
        isFetching: results.some((r) => r.isFetching),
        isError: results.some((r) => r.isError),
    };
}
