// Clay endpoints, scoped to one connection id.

import type { ClaySettings, ClaySettingsUpdate } from "@/lib/api/models/app/integrations/Clay";
import Request from "../../Request";

export async function getClaySettings(id: string): Promise<ClaySettings> {
    return await Request<ClaySettings>({ method: "GET", url: `/integrations/clay/${id}`, authorization: true });
}

export async function updateClaySettings(input: { connectionId: string; body: ClaySettingsUpdate }): Promise<ClaySettings> {
    return await Request<ClaySettings>({
        method: "PATCH",
        url: `/integrations/clay/${input.connectionId}`,
        data: input.body,
        authorization: true,
    });
}
