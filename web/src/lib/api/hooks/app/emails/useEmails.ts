import { useMemo } from "react";
import { infiniteQueryOptions, useInfiniteQuery, type InfiniteData } from "@tanstack/react-query";
import type GetEmails from "@/lib/api/models/app/emails/GetEmails";
import getEmails from "@/lib/api/client/app/emails/getEmails";
import useAllPages from "@/lib/api/hooks/useAllPages";

// Pages are fetched until the list is whole, so a larger page means fewer round trips.
const EMAILS_PAGE_LIMIT = 200;

interface UseEmailsProps {
    query: string;
    tag: string;
    limit?: number;
    enabled?: boolean;
}

export const emailsListQuery = ({ query, tag, limit = EMAILS_PAGE_LIMIT }: Omit<UseEmailsProps, "enabled">) =>
    infiniteQueryOptions<
        GetEmails,
        Error,
        InfiniteData<GetEmails, string | null>,
        [string, string, string, string, number],
        string | null
    >({
        queryKey: ["emails", "list", query, tag, limit],
        queryFn: async ({ pageParam }) => getEmails(query, pageParam, tag, limit),
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

// Every caller counts, selects or picks from the whole list, so this keeps paging until it is complete.
export default function useEmails({ query, tag, limit = EMAILS_PAGE_LIMIT, enabled = true }: UseEmailsProps) {
    const queryResult = useInfiniteQuery({
        ...emailsListQuery({ query, tag, limit }),
        enabled,
    });

    const rest = useAllPages(queryResult, enabled);
    const emails = useMemo(() => queryResult.data?.pages.flatMap((p) => p.data) ?? [], [queryResult.data]);

    return {
        ...queryResult,
        emails,
        ...rest,
    };
}
