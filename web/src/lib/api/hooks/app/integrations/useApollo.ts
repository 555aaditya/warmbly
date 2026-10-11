import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
    createApolloImportSource,
    deleteApolloImportSource,
    getApolloOverview,
    listApolloEnrichments,
    listApolloImportSources,
    listApolloLists,
    previewApolloImport,
    replaceApolloKey,
    runApolloImportSource,
    startApolloEnrichment,
    updateApolloImportSource,
} from "@/lib/api/client/app/integrations/apollo";
import type { ApolloImportSource } from "@/lib/api/models/app/integrations/Apollo";

// Under the "integrations" prefix the realtime spine refreshes for entity_type "integration".
export const APOLLO_KEY = ["integrations", "apollo"] as const;

const apKey = (id: string, ...rest: unknown[]) => [...APOLLO_KEY, id, ...rest];

export const apolloOverviewQuery = (id: string) =>
    queryOptions({ queryKey: apKey(id, "overview"), queryFn: () => getApolloOverview(id), staleTime: 15_000 });

export const apolloSourcesQuery = (id: string) =>
    queryOptions({ queryKey: apKey(id, "import-sources"), queryFn: () => listApolloImportSources(id), staleTime: 10_000 });

export const apolloEnrichmentsQuery = (id: string) =>
    queryOptions({ queryKey: apKey(id, "enrichments"), queryFn: () => listApolloEnrichments(id), staleTime: 10_000 });

export function useApolloOverview(id: string) {
    return useQuery({ ...apolloOverviewQuery(id), enabled: !!id });
}

export function useReplaceApolloKey(id: string) {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: replaceApolloKey,
        onSuccess: (ov) => {
            qc.setQueryData(apKey(id, "overview"), ov);
            qc.invalidateQueries({ queryKey: ["integrations", "connections"] });
            qc.invalidateQueries({ queryKey: ["apollo-lookup", id] });
        },
    });
}

// The account's lists cost an Apollo call, so they live outside the spine prefix.
export function useApolloLists(id: string, enabled = true) {
    return useQuery({
        queryKey: ["apollo-lookup", id, "lists"],
        queryFn: () => listApolloLists(id),
        enabled: !!id && enabled,
        staleTime: 5 * 60 * 1000,
        retry: false,
    });
}

export function useApolloImportPreview() {
    return useMutation({ mutationFn: previewApolloImport });
}

// No realtime event marks a run finishing, so the list polls only while one runs.
export function useApolloImportSources(id: string) {
    return useQuery({
        ...apolloSourcesQuery(id),
        enabled: !!id,
        refetchInterval: (q) => ((q.state.data ?? []).some((s) => s.status === "running") ? 3000 : false),
    });
}

function useInvalidateSources(id: string) {
    const qc = useQueryClient();
    return () => {
        qc.invalidateQueries({ queryKey: apKey(id, "import-sources") });
        qc.invalidateQueries({ queryKey: apKey(id, "overview") });
    };
}

export function useCreateApolloImportSource(id: string) {
    const refresh = useInvalidateSources(id);
    const qc = useQueryClient();
    return useMutation({
        mutationFn: createApolloImportSource,
        onSuccess: () => {
            refresh();
            qc.invalidateQueries({ queryKey: ["contacts"] });
        },
    });
}

// Pausing, resuming and renaming are predictable, so the row changes at once.
export function useUpdateApolloImportSource(id: string) {
    const qc = useQueryClient();
    const refresh = useInvalidateSources(id);
    return useMutation({
        mutationFn: updateApolloImportSource,
        onMutate: async (input) => {
            const key = apKey(id, "import-sources");
            await qc.cancelQueries({ queryKey: key });
            const prev = qc.getQueryData<ApolloImportSource[]>(key);
            if (prev) {
                qc.setQueryData<ApolloImportSource[]>(
                    key,
                    prev.map((s) => (s.id === input.sourceId ? ({ ...s, ...input.body } as ApolloImportSource) : s)),
                );
            }
            return { prev };
        },
        onError: (_err, _input, ctx) => {
            if (ctx?.prev) qc.setQueryData(apKey(id, "import-sources"), ctx.prev);
        },
        onSettled: refresh,
    });
}

export function useRunApolloImportSource(id: string) {
    const refresh = useInvalidateSources(id);
    return useMutation({ mutationFn: runApolloImportSource, onSuccess: refresh });
}

export function useDeleteApolloImportSource(id: string) {
    const qc = useQueryClient();
    const refresh = useInvalidateSources(id);
    return useMutation({
        mutationFn: deleteApolloImportSource,
        onMutate: async (input) => {
            const key = apKey(id, "import-sources");
            await qc.cancelQueries({ queryKey: key });
            const prev = qc.getQueryData<ApolloImportSource[]>(key);
            if (prev) qc.setQueryData<ApolloImportSource[]>(key, prev.filter((s) => s.id !== input.sourceId));
            return { prev };
        },
        onError: (_err, _input, ctx) => {
            if (ctx?.prev) qc.setQueryData(apKey(id, "import-sources"), ctx.prev);
        },
        onSettled: refresh,
    });
}

// Polls while a run is queued or running; nothing else reports its progress.
export function useApolloEnrichments(id: string) {
    return useQuery({
        ...apolloEnrichmentsQuery(id),
        enabled: !!id,
        refetchInterval: (q) =>
            (q.state.data ?? []).some((r) => r.status === "queued" || r.status === "running") ? 3000 : false,
    });
}

export function useStartApolloEnrichment() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: startApolloEnrichment,
        onSuccess: (run) => {
            qc.invalidateQueries({ queryKey: apKey(run.connection_id, "enrichments") });
        },
    });
}
