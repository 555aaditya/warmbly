// Apollo endpoints, all scoped to one connection id.

import type {
    ApolloEnrichmentRun,
    ApolloImportSource,
    ApolloList,
    ApolloOverview,
    ApolloPreview,
    CreateApolloImportSourceInput,
    UpdateApolloImportSourceInput,
} from "@/lib/api/models/app/integrations/Apollo";
import type ContactSelection from "@/lib/api/models/app/contacts/ContactSelection";
import Request from "../../Request";

const base = (id: string) => `/integrations/apollo/${id}`;

export async function getApolloOverview(id: string): Promise<ApolloOverview> {
    return await Request<ApolloOverview>({ method: "GET", url: base(id), authorization: true });
}

export async function replaceApolloKey(input: { connectionId: string; apiKey: string }): Promise<ApolloOverview> {
    return await Request<ApolloOverview>({
        method: "PUT",
        url: `${base(input.connectionId)}/key`,
        data: { api_key: input.apiKey },
        authorization: true,
    });
}

export async function listApolloLists(id: string): Promise<ApolloList[]> {
    const body = await Request<{ data: ApolloList[] }>({ method: "GET", url: `${base(id)}/lists`, authorization: true });
    return body?.data ?? [];
}

export async function previewApolloImport(input: {
    connectionId: string;
    list_id: string;
    verified_only: boolean;
}): Promise<ApolloPreview> {
    const { connectionId, ...body } = input;
    return await Request<ApolloPreview>({
        method: "POST",
        url: `${base(connectionId)}/import/preview`,
        data: body,
        authorization: true,
    });
}

export async function listApolloImportSources(id: string): Promise<ApolloImportSource[]> {
    const body = await Request<{ data: ApolloImportSource[] }>({
        method: "GET",
        url: `${base(id)}/import-sources`,
        authorization: true,
    });
    return body?.data ?? [];
}

export async function createApolloImportSource(input: {
    connectionId: string;
    body: CreateApolloImportSourceInput;
}): Promise<ApolloImportSource> {
    return await Request<ApolloImportSource>({
        method: "POST",
        url: `${base(input.connectionId)}/import-sources`,
        data: input.body,
        authorization: true,
    });
}

export async function updateApolloImportSource(input: {
    connectionId: string;
    sourceId: string;
    body: UpdateApolloImportSourceInput;
}): Promise<ApolloImportSource> {
    return await Request<ApolloImportSource>({
        method: "PATCH",
        url: `${base(input.connectionId)}/import-sources/${input.sourceId}`,
        data: input.body,
        authorization: true,
    });
}

export async function runApolloImportSource(input: { connectionId: string; sourceId: string }): Promise<ApolloImportSource> {
    return await Request<ApolloImportSource>({
        method: "POST",
        url: `${base(input.connectionId)}/import-sources/${input.sourceId}/run`,
        authorization: true,
    });
}

export async function deleteApolloImportSource(input: { connectionId: string; sourceId: string }): Promise<void> {
    await Request<void>({
        method: "DELETE",
        url: `${base(input.connectionId)}/import-sources/${input.sourceId}`,
        authorization: true,
    });
}

export async function listApolloEnrichments(id: string): Promise<ApolloEnrichmentRun[]> {
    const body = await Request<{ data: ApolloEnrichmentRun[] }>({
        method: "GET",
        url: `${base(id)}/enrichments`,
        authorization: true,
    });
    return body?.data ?? [];
}

export async function startApolloEnrichment(input: {
    connectionId: string;
    selection: ContactSelection;
    overwrite: boolean;
}): Promise<ApolloEnrichmentRun> {
    return await Request<ApolloEnrichmentRun>({
        method: "POST",
        url: `${base(input.connectionId)}/enrichments`,
        data: { ...input.selection, overwrite: input.overwrite },
        authorization: true,
    });
}
