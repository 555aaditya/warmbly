import { useMemo } from "react";
import { infiniteQueryOptions, useInfiniteQuery, type InfiniteData } from "@tanstack/react-query";
import getCampaigns from "@/lib/api/client/app/campaigns/getCampaigns";
import useAllPages from "@/lib/api/hooks/useAllPages";
import type GetCampaigns from "@/lib/api/models/app/campaigns/GetCampaigns";
import useRealtimeFallbackInterval from "@/hooks/useRealtimeFallback";

// Pages are fetched until the list is whole, so a larger page means fewer round trips.
const CAMPAIGNS_PAGE_LIMIT = 200;

interface UseCampaignsProps {
    query: string;
    folder: string;
    limit?: number;
    enabled?: boolean;
    /** Page to the end; off for a search picker that only shows the top matches. */
    all?: boolean;
}

export const campaignsListQuery = ({ query, folder, limit = CAMPAIGNS_PAGE_LIMIT }: Pick<UseCampaignsProps, "query" | "folder" | "limit">) =>
    infiniteQueryOptions<
        GetCampaigns,
        Error,
        InfiniteData<GetCampaigns, string | null>,
        [string, string, string, string, number],
        string | null
    >({
        queryKey: ["campaigns", "list", query, folder, limit],
        queryFn: async ({ pageParam }) => getCampaigns(query, pageParam, folder, limit),
        initialPageParam: null,
        getNextPageParam: (lastPage) => {
            if (lastPage.pagination.has_more) {
                return lastPage.pagination.next_cursor
            }
            return undefined
        },
        staleTime: 5 * 60 * 1000,
        gcTime: 10 * 60 * 1000,
    });

export default function useCampaigns({ query, folder, limit = CAMPAIGNS_PAGE_LIMIT, enabled = true, all = true }: UseCampaignsProps) {
    // Send counts on these cards move on realtime invalidation, so the long
    // staleTime below is free while the socket is up and strands the list for
    // five minutes when it is not. Poll only in that second case.
    const refetchInterval = useRealtimeFallbackInterval(enabled);
    const queryResult = useInfiniteQuery({
        ...campaignsListQuery({ query, folder, limit }),
        refetchInterval,
        enabled,
    });

    const rest = useAllPages(queryResult, enabled && all);

    // Defensive: backend may return `data: null` on empty result sets if
    // the underlying slice was nil. Coerce + drop nulls so consumers can
    // safely read fields without optional-chaining every access.
    const campaigns = useMemo(
        () => queryResult.data?.pages
            .flatMap((p) => p.data ?? [])
            .filter((c): c is NonNullable<typeof c> => c != null) ?? [],
        [queryResult.data],
    );

    return {
        ...queryResult,
        campaigns,
        ...rest,
    };
}
