import { useMemo } from "react";
import { infiniteQueryOptions, useInfiniteQuery, type InfiniteData } from "@tanstack/react-query";
import type SearchTasks from "@/lib/api/models/app/crm/SearchTasks";
import type TasksSearchResult from "@/lib/api/models/app/crm/TasksSearchResult";
import searchTasks from "@/lib/api/client/app/crm/tasks/searchTasks";

interface UseSearchTasksProps {
    filters: SearchTasks;
    limit?: number;
    enabled?: boolean;
}

export const tasksSearchQuery = ({ filters, limit = 50 }: Omit<UseSearchTasksProps, "enabled">) =>
    infiniteQueryOptions<
        TasksSearchResult,
        Error,
        InfiniteData<TasksSearchResult, string | undefined>,
        [string, string, string, SearchTasks, number],
        string | undefined
    >({
        queryKey: ["crm", "tasks", "search", filters, limit],
        queryFn: async ({ pageParam }) => searchTasks(filters, pageParam, limit),
        initialPageParam: undefined,
        getNextPageParam: (lastPage) =>
            lastPage.pagination.has_more ? (lastPage.pagination.next_cursor ?? undefined) : undefined,
        staleTime: 30_000,
    });

// Server-driven tasks fetch that scales to thousands of rows. Offset pagination
// under the hood (so nullable due-date sorts don't drop rows), but the page param
// is the same opaque next_cursor every other list uses. Pages flatten into a
// single `tasks` list and `total` comes straight off the server so the UI can
// show "N of M loaded".
export default function useSearchTasks({ filters, limit = 50, enabled = true }: UseSearchTasksProps) {
    const queryResult = useInfiniteQuery({
        ...tasksSearchQuery({ filters, limit }),
        enabled,
    });

    const tasks = useMemo(
        () => queryResult.data?.pages
            .flatMap((p) => p.data ?? [])
            .filter((t): t is NonNullable<typeof t> => t != null),
        [queryResult.data],
    );

    const total = queryResult.data?.pages[0]?.pagination.total ?? 0;

    return { ...queryResult, tasks, total };
}
