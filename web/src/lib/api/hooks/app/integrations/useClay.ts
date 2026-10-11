import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getClaySettings, updateClaySettings } from "@/lib/api/client/app/integrations/clay";
import type { ClaySettings } from "@/lib/api/models/app/integrations/Clay";

// Under the "integrations" prefix the realtime spine refreshes for entity_type "integration".
export const CLAY_KEY = ["integrations", "clay"] as const;

export const claySettingsQuery = (id: string) =>
    queryOptions({ queryKey: [...CLAY_KEY, id], queryFn: () => getClaySettings(id), staleTime: 15_000 });

export function useClaySettings(id: string) {
    return useQuery({ ...claySettingsQuery(id), enabled: !!id });
}

export function useUpdateClaySettings(id: string) {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: updateClaySettings,
        onSuccess: (s: ClaySettings) => {
            qc.setQueryData([...CLAY_KEY, id], s);
            qc.invalidateQueries({ queryKey: ["integrations", "connections"] });
        },
    });
}
