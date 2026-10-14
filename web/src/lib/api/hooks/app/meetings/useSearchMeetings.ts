import { useMemo } from "react";
import { infiniteQueryOptions, useInfiniteQuery, type InfiniteData } from "@tanstack/react-query";
import type { MeetingsPage, MeetingsSearch } from "@/lib/api/models/app/integrations/Integration";
import searchMeetings from "@/lib/api/client/app/meetings/searchMeetings";

interface UseSearchMeetingsProps {
    filters: MeetingsSearch;
    limit?: number;
    enabled?: boolean;
}

export const meetingsSearchQuery = ({ filters, limit = 50 }: Omit<UseSearchMeetingsProps, "enabled">) =>
    infiniteQueryOptions<
        MeetingsPage,
        Error,
        InfiniteData<MeetingsPage, string | undefined>,
        [string, string, MeetingsSearch, number],
        string | undefined
    >({
        queryKey: ["meetings", "search", filters, limit],
        queryFn: async ({ pageParam }) => searchMeetings(filters, pageParam, limit),
        initialPageParam: undefined,
        getNextPageParam: (lastPage) =>
            lastPage.pagination.has_more ? (lastPage.pagination.next_cursor ?? undefined) : undefined,
        staleTime: 15_000,
    });

// Infinite scroll over booked meetings, paginated by the same opaque next_cursor
// every other list uses. Pages flatten into one list; `total` comes off the
// server so the UI can show "N of M".
export default function useSearchMeetings({ filters, limit = 50, enabled = true }: UseSearchMeetingsProps) {
    const queryResult = useInfiniteQuery({
        ...meetingsSearchQuery({ filters, limit }),
        enabled,
    });

    const meetings = useMemo(
        () => queryResult.data?.pages
            .flatMap((p) => p.data ?? [])
            .filter((m): m is NonNullable<typeof m> => m != null),
        [queryResult.data],
    );

    const total = queryResult.data?.pages[0]?.pagination.total ?? 0;

    return { ...queryResult, meetings, total };
}
