import { useMemo } from "react";
import { infiniteQueryOptions, useInfiniteQuery, type InfiniteData } from "@tanstack/react-query";
import type SearchDeals from "@/lib/api/models/app/crm/SearchDeals";
import type DealsSearchResult from "@/lib/api/models/app/crm/DealsSearchResult";
import searchDeals from "@/lib/api/client/app/crm/deals/searchDeals";

interface UseSearchDealsProps {
    filters: SearchDeals;
    limit?: number;
    enabled?: boolean;
}

export const dealsSearchQuery = ({ filters, limit = 50 }: Omit<UseSearchDealsProps, "enabled">) =>
    infiniteQueryOptions<
        DealsSearchResult,
        Error,
        InfiniteData<DealsSearchResult, string | undefined>,
        [string, string, string, SearchDeals, number],
        string | undefined
    >({
        queryKey: ["crm", "deals", "search", filters, limit],
        queryFn: async ({ pageParam }) => searchDeals(filters, pageParam, limit),
        initialPageParam: undefined,
        getNextPageParam: (lastPage) =>
            lastPage.pagination.has_more ? (lastPage.pagination.next_cursor ?? undefined) : undefined,
        staleTime: 30_000,
    });

// Cross-pipeline deals fetch. Offset pagination under the hood (so nullable
// value/close-date sorts don't drop rows), but the page param is the same opaque
// next_cursor every other list uses. Pages flatten into a single `deals` list
// and `total` comes straight off the server so the UI can show "N of M".
export default function useSearchDeals({ filters, limit = 50, enabled = true }: UseSearchDealsProps) {
    const queryResult = useInfiniteQuery({
        ...dealsSearchQuery({ filters, limit }),
        enabled,
    });

    const deals = useMemo(
        () => queryResult.data?.pages
            .flatMap((p) => p.data ?? [])
            .filter((d): d is NonNullable<typeof d> => d != null),
        [queryResult.data],
    );

    const total = queryResult.data?.pages[0]?.pagination.total ?? 0;

    return { ...queryResult, deals, total };
}
